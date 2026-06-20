package main

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const maxPDFSize = 8 << 20

// studentMyCoursesHandler 列出学生选了的课程。
func studentMyCoursesHandler(c *gin.Context) {
	ses := currentSession(c)
	if ses == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "未登录"})
		return
	}
	rows, err := db.Query(`
SELECT c.CourseID, c.CourseName, t.TeacherID, a.DisplayName, c.Semester, c.FolderName, c.CreatedAt
FROM Course c
JOIN CourseStudent cs ON cs.CourseID=c.CourseID
JOIN Student s ON s.SNO=cs.SNO
JOIN Teacher t ON t.TeacherID=c.TeacherID
JOIN AppUser a ON a.UserID=t.UserID
WHERE s.UserID=@p1
ORDER BY c.CreatedAt DESC`, ses.UserID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	var list []Course
	for rows.Next() {
		var item Course
		if err := rows.Scan(&item.ID, &item.Name, &item.TeacherID, &item.Teacher, &item.Semester, &item.FolderName, &item.CreatedAt); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		list = append(list, item)
	}
	c.JSON(http.StatusOK, list)
}

// studentLabsHandler 列出学生在某课程下的所有实验项目（含自己的提交状态）。
func studentLabsHandler(c *gin.Context) {
	ses := currentSession(c)
	if ses == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "未登录"})
		return
	}
	courseID, ok := parseIDParam(c.Param("courseID"))
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ID 无效"})
		return
	}
	// 校验：该学生确实选了这门课
	var sno string
	err := db.QueryRow(`
SELECT s.SNO FROM Student s
JOIN CourseStudent cs ON cs.SNO=s.SNO
WHERE s.UserID=@p1 AND cs.CourseID=@p2`, ses.UserID, courseID).Scan(&sno)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "未选该课程"})
		return
	}

	rows, err := db.Query(`
SELECT l.LabID, l.CourseID, l.LabName, ISNULL(l.Description,''), l.Status,
       l.OpenAt, l.Deadline, l.FolderName, l.CreatedAt,
       CASE WHEN r.ReportID IS NULL THEN 0 ELSE 1 END AS Submitted,
       0,
       r.Score, ISNULL(r.Comment,''), r.GradedAt, r.SubmittedAt
FROM LabProject l
LEFT JOIN Report r ON r.LabID=l.LabID AND r.SNO=@p1
WHERE l.CourseID=@p2
ORDER BY l.LabID`, sno, courseID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	var labs []Lab
	for rows.Next() {
		var item Lab
		if err := rows.Scan(&item.ID, &item.CourseID, &item.Name, &item.Description, &item.Status,
			&item.OpenAt, &item.Deadline, &item.FolderName, &item.CreatedAt,
			&item.Submitted, &item.StudentCnt,
			&item.MyScore, &item.MyComment, &item.MyGradedAt, &item.MySubmittedAt); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		labs = append(labs, item)
	}
	c.JSON(http.StatusOK, labs)
}

// studentMyReportHandler 查询自己在某实验项目的报告。
func studentMyReportHandler(c *gin.Context) {
	ses := currentSession(c)
	if ses == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "未登录"})
		return
	}
	labID, ok := parseIDParam(c.Param("labID"))
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ID 无效"})
		return
	}
	var sno string
	if err := db.QueryRow(`SELECT SNO FROM Student WHERE UserID=@p1`, ses.UserID).Scan(&sno); err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "非学生账户"})
		return
	}
	var r Report
	err := db.QueryRow(`
SELECT r.ReportID, r.LabID, l.LabName, c.CourseName, r.SNO, s.SName,
       r.OriginalName, r.StoredName, r.SizeBytes, r.SubmittedAt, r.Score, ISNULL(r.Comment,''), r.GradedAt
FROM Report r
JOIN Student s ON s.SNO=r.SNO
JOIN LabProject l ON l.LabID=r.LabID
JOIN Course c ON c.CourseID=l.CourseID
WHERE r.LabID=@p1 AND r.SNO=@p2`, labID, sno).
		Scan(&r.ID, &r.LabID, &r.LabName, &r.CourseName, &r.SNO, &r.StudentName, &r.OriginalName, &r.StoredName, &r.SizeBytes, &r.SubmittedAt, &r.Score, &r.Comment, &r.GradedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusOK, nil)
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, r)
}

// uploadReportHandler 学生上传实验报告 PDF。
// 关键流程：校验登录 -> 校验选课 -> 校验项目开放 -> 校验未过截止 -> 保存为 学号-姓名-实验名.pdf -> 事务写表
func uploadReportHandler(c *gin.Context) {
	ses := currentSession(c)
	if ses == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "未登录"})
		return
	}
	labID, ok := parseIDParam(c.Param("labID"))
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ID 无效"})
		return
	}
	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请上传 PDF 文件"})
		return
	}
	if file.Size > maxPDFSize {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "PDF 文件不能超过 8MB"})
		return
	}
	if !strings.HasSuffix(strings.ToLower(file.Filename), ".pdf") {
		c.JSON(http.StatusUnsupportedMediaType, gin.H{"error": "仅支持 PDF 文件"})
		return
	}

	// 双重校验：必须是登录学生本人
	var sno, sname string
	if err := db.QueryRow(`SELECT SNO, SName FROM Student WHERE UserID=@p1`, ses.UserID).Scan(&sno, &sname); err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "非学生账户"})
		return
	}

	// 取出实验项目和课程信息
	var (
		labName      string
		labFolder    string
		courseFolder string
		status       string
		courseID     int64
		openAt       sql.NullTime
		deadline     sql.NullTime
	)
	err = db.QueryRow(`
SELECT l.LabName, l.FolderName, c.FolderName, l.Status, l.CourseID, l.OpenAt, l.Deadline
FROM LabProject l JOIN Course c ON c.CourseID=l.CourseID
WHERE l.LabID=@p1`, labID).Scan(&labName, &labFolder, &courseFolder, &status, &courseID, &openAt, &deadline)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "实验项目不存在"})
		return
	}
	if status != "open" {
		c.JSON(http.StatusForbidden, gin.H{"error": "该实验项目未开放上传"})
		return
	}
	if deadline.Valid && time.Now().UTC().After(deadline.Time) {
		c.JSON(http.StatusForbidden, gin.H{"error": "已超过截止时间"})
		return
	}

	// 校验选课名单
	var enrolled int
	if err := db.QueryRow(`SELECT COUNT(*) FROM CourseStudent WHERE CourseID=@p1 AND SNO=@p2`, courseID, sno).Scan(&enrolled); err != nil || enrolled == 0 {
		c.JSON(http.StatusForbidden, gin.H{"error": "该学生不在本课程名单"})
		return
	}

	// 统一文件名：<学号>-<姓名>-<实验项目名>.pdf
	storedName := fmt.Sprintf("%s-%s-%s.pdf", sno, safeName(sname), safeName(labName))
	dir := uploadDirFor(courseFolder, labFolder)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	savePath := filepath.Join(dir, storedName)
	if err := c.SaveUploadedFile(file, savePath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 事务写库：覆盖式 UPSERT。重新上传会重置评分。
	tx, err := db.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
MERGE Report AS target
USING (SELECT @p1 AS LabID, @p2 AS SNO) AS src
ON target.LabID=src.LabID AND target.SNO=src.SNO
WHEN MATCHED THEN UPDATE SET
    OriginalName=@p3, StoredName=@p4, FilePath=@p5, SizeBytes=@p6,
    SubmittedAt=SYSUTCDATETIME(), Score=NULL, Comment=NULL, GradedAt=NULL, GradedBy=NULL
WHEN NOT MATCHED THEN INSERT
    (LabID, SNO, OriginalName, StoredName, FilePath, SizeBytes)
    VALUES (@p1, @p2, @p3, @p4, @p5, @p6);`,
		labID, sno, file.Filename, storedName, savePath, file.Size); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	audit(ses.UserID, "upload_report", fmt.Sprintf("lab=%d sno=%s", labID, sno))
	c.JSON(http.StatusOK, gin.H{"message": "上传成功", "filename": storedName, "size": file.Size})
}

// studentDownloadOwnHandler 学生下载自己刚上传的 PDF（便于复核）。
func studentDownloadOwnHandler(c *gin.Context) {
	ses := currentSession(c)
	if ses == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "未登录"})
		return
	}
	labID, ok := parseIDParam(c.Param("labID"))
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ID 无效"})
		return
	}
	var (
		storedName string
		filePath   string
	)
	err := db.QueryRow(`
SELECT r.StoredName, r.FilePath FROM Report r
JOIN Student s ON s.SNO=r.SNO
WHERE r.LabID=@p1 AND s.UserID=@p2`, labID, ses.UserID).Scan(&storedName, &filePath)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "尚未上传"})
		return
	}
	c.FileAttachment(filePath, storedName)
}
