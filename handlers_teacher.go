package main

import (
	"archive/zip"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/xuri/excelize/v2"
)

const maxExcelSize = 10 << 20

// importCourseHandler 教师上传 Excel 创建/更新课程。
func importCourseHandler(c *gin.Context) {
	ses := currentSession(c)
	if ses == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "未登录"})
		return
	}
	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请上传 Excel 文件"})
		return
	}
	if file.Size > maxExcelSize {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "Excel 文件过大"})
		return
	}
	if !strings.HasSuffix(strings.ToLower(file.Filename), ".xlsx") {
		c.JSON(http.StatusUnsupportedMediaType, gin.H{"error": "仅支持 .xlsx 文件"})
		return
	}

	courseName := strings.TrimSpace(c.PostForm("courseName"))
	semester := strings.TrimSpace(c.PostForm("semester"))

	dir := filepath.Join(uploadRoot, "imports")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	tmpPath := filepath.Join(dir, fmt.Sprintf("%d_%s", time.Now().UnixNano(), safeName(file.Filename)))
	if err := c.SaveUploadedFile(file, tmpPath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	summary, err := importExcelCourse(tmpPath, courseName, semester, ses.UserID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	audit(ses.UserID, "import_course", fmt.Sprintf("%s/%s 学生%d 实验%d", summary.CourseName, summary.Semester, summary.Students, summary.Labs))
	c.JSON(http.StatusOK, summary)
}

// teacherListCoursesHandler 列出课程：教师只看自己负责的，管理员看全部。
func teacherListCoursesHandler(c *gin.Context) {
	ses := currentSession(c)
	if ses == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "未登录"})
		return
	}
	var (
		rows *sql.Rows
		err  error
	)
	if ses.Role == "admin" {
		rows, err = db.Query(`
SELECT c.CourseID, c.CourseName, t.TeacherID, a.DisplayName, c.Semester, c.FolderName, c.CreatedAt,
       (SELECT COUNT(*) FROM CourseStudent cs WHERE cs.CourseID=c.CourseID),
       (SELECT COUNT(*) FROM LabProject l WHERE l.CourseID=c.CourseID)
FROM Course c JOIN Teacher t ON t.TeacherID=c.TeacherID JOIN AppUser a ON a.UserID=t.UserID
ORDER BY c.CreatedAt DESC`)
	} else {
		rows, err = db.Query(`
SELECT c.CourseID, c.CourseName, t.TeacherID, a.DisplayName, c.Semester, c.FolderName, c.CreatedAt,
       (SELECT COUNT(*) FROM CourseStudent cs WHERE cs.CourseID=c.CourseID),
       (SELECT COUNT(*) FROM LabProject l WHERE l.CourseID=c.CourseID)
FROM Course c JOIN Teacher t ON t.TeacherID=c.TeacherID JOIN AppUser a ON a.UserID=t.UserID
WHERE t.UserID=@p1
ORDER BY c.CreatedAt DESC`, ses.UserID)
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	var list []Course
	for rows.Next() {
		var item Course
		if err := rows.Scan(&item.ID, &item.Name, &item.TeacherID, &item.Teacher, &item.Semester, &item.FolderName, &item.CreatedAt, &item.Students, &item.Labs); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		list = append(list, item)
	}
	c.JSON(http.StatusOK, list)
}

// ensureCourseOwnedByTeacher 校验课程归属当前教师，admin 直接放行。
func ensureCourseOwnedByTeacher(c *gin.Context, courseID int64) bool {
	ses := currentSession(c)
	if ses == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "未登录"})
		return false
	}
	if ses.Role == "admin" {
		return true
	}
	var ownerUserID int64
	err := db.QueryRow(`SELECT t.UserID FROM Course c JOIN Teacher t ON t.TeacherID=c.TeacherID WHERE c.CourseID=@p1`, courseID).Scan(&ownerUserID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "课程不存在"})
		return false
	}
	if ownerUserID != ses.UserID {
		c.JSON(http.StatusForbidden, gin.H{"error": "无权访问该课程"})
		return false
	}
	return true
}

func ensureLabOwnedByTeacher(c *gin.Context, labID int64) (courseID int64, ok bool) {
	ses := currentSession(c)
	if ses == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "未登录"})
		return 0, false
	}
	var (
		ownerUserID int64
		cID         int64
	)
	err := db.QueryRow(`
SELECT t.UserID, l.CourseID
FROM LabProject l JOIN Course c ON c.CourseID=l.CourseID
JOIN Teacher t ON t.TeacherID=c.TeacherID
WHERE l.LabID=@p1`, labID).Scan(&ownerUserID, &cID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "实验项目不存在"})
		return 0, false
	}
	if ses.Role != "admin" && ownerUserID != ses.UserID {
		c.JSON(http.StatusForbidden, gin.H{"error": "无权操作该实验项目"})
		return 0, false
	}
	return cID, true
}

// listLabsHandler 列出某课程下的所有实验项目。
func listLabsHandler(c *gin.Context) {
	courseID, ok := parseIDParam(c.Param("courseID"))
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ID 无效"})
		return
	}
	if !ensureCourseOwnedByTeacher(c, courseID) {
		return
	}
	rows, err := db.Query(`
SELECT l.LabID, l.CourseID, l.LabName, ISNULL(l.Description,''), l.Status, l.OpenAt, l.Deadline, l.FolderName, l.CreatedAt,
       (SELECT COUNT(*) FROM CourseStudent cs WHERE cs.CourseID=l.CourseID),
       (SELECT COUNT(*) FROM Report r WHERE r.LabID=l.LabID)
FROM LabProject l
WHERE l.CourseID=@p1
ORDER BY l.LabID`, courseID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	var labs []Lab
	for rows.Next() {
		var item Lab
		if err := rows.Scan(&item.ID, &item.CourseID, &item.Name, &item.Description, &item.Status, &item.OpenAt, &item.Deadline, &item.FolderName, &item.CreatedAt, &item.StudentCnt, &item.Submitted); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		labs = append(labs, item)
	}
	c.JSON(http.StatusOK, labs)
}

// listStudentsHandler 列出课程内的学生名单。
func listStudentsHandler(c *gin.Context) {
	courseID, ok := parseIDParam(c.Param("courseID"))
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ID 无效"})
		return
	}
	if !ensureCourseOwnedByTeacher(c, courseID) {
		return
	}
	rows, err := db.Query(`
SELECT s.SNO, s.SName, ISNULL(s.ClassName,'')
FROM Student s JOIN CourseStudent cs ON cs.SNO=s.SNO
WHERE cs.CourseID=@p1
ORDER BY s.SNO`, courseID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	var list []Student
	for rows.Next() {
		var s Student
		if err := rows.Scan(&s.SNO, &s.Name, &s.ClassName); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		list = append(list, s)
	}
	c.JSON(http.StatusOK, list)
}

// updateLabHandler 修改实验项目状态、截止时间、描述。
func updateLabHandler(c *gin.Context) {
	labID, ok := parseIDParam(c.Param("labID"))
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ID 无效"})
		return
	}
	if _, ok := ensureLabOwnedByTeacher(c, labID); !ok {
		return
	}
	var body struct {
		Status      string `json:"status"`
		Deadline    string `json:"deadline"`
		Description string `json:"description"`
	}
	if err := c.BindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体格式错误"})
		return
	}
	if body.Status != "open" && body.Status != "closed" && body.Status != "ended" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "状态必须为 open/closed/ended"})
		return
	}

	var deadline any
	if d := strings.TrimSpace(body.Deadline); d != "" {
		t, err := time.Parse(time.RFC3339, d)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "截止时间格式错误，应为 RFC3339"})
			return
		}
		deadline = t.UTC()
	} else {
		deadline = nil
	}

	tx, err := db.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer tx.Rollback()

	var prevStatus string
	if err := tx.QueryRow(`SELECT Status FROM LabProject WHERE LabID=@p1`, labID).Scan(&prevStatus); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	openAt := sql.NullTime{}
	if body.Status == "open" && prevStatus != "open" {
		openAt = sql.NullTime{Time: time.Now().UTC(), Valid: true}
	}

	desc := strings.TrimSpace(body.Description)
	if openAt.Valid {
		if _, err := tx.Exec(`UPDATE LabProject SET Status=@p1, Deadline=@p2, Description=@p3, OpenAt=@p4 WHERE LabID=@p5`,
			body.Status, deadline, desc, openAt.Time, labID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	} else {
		if _, err := tx.Exec(`UPDATE LabProject SET Status=@p1, Deadline=@p2, Description=@p3 WHERE LabID=@p4`,
			body.Status, deadline, desc, labID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if ses := currentSession(c); ses != nil {
		audit(ses.UserID, "update_lab", fmt.Sprintf("lab=%d status=%s", labID, body.Status))
	}
	c.JSON(http.StatusOK, gin.H{"message": "实验项目已更新"})
}

// deleteLabHandler 删除实验项目：连带级联删除报告记录与磁盘上的报告文件。
func deleteLabHandler(c *gin.Context) {
	labID, ok := parseIDParam(c.Param("labID"))
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ID 无效"})
		return
	}
	if _, ok := ensureLabOwnedByTeacher(c, labID); !ok {
		return
	}

	var (
		labName      string
		labFolder    string
		courseFolder string
		reportCount  int
	)
	err := db.QueryRow(`
SELECT l.LabName, l.FolderName, c.FolderName,
       (SELECT COUNT(*) FROM Report r WHERE r.LabID=l.LabID)
FROM LabProject l JOIN Course c ON c.CourseID=l.CourseID
WHERE l.LabID=@p1`, labID).Scan(&labName, &labFolder, &courseFolder, &reportCount)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "实验项目不存在"})
		return
	}

	// 删除 DB 记录（外键 ON DELETE CASCADE 自动清理 Report）
	if _, err := db.Exec(`DELETE FROM LabProject WHERE LabID=@p1`, labID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 删除磁盘目录（即使没有报告，目录也可能存在）
	dir := uploadDirFor(courseFolder, labFolder)
	_ = os.RemoveAll(dir)

	if ses := currentSession(c); ses != nil {
		audit(ses.UserID, "delete_lab", fmt.Sprintf("lab=%d name=%s reports=%d", labID, labName, reportCount))
	}
	c.JSON(http.StatusOK, gin.H{"message": "实验项目已删除", "removedReports": reportCount})
}

// listReportsHandler 列出某实验项目的所有报告。
func listReportsHandler(c *gin.Context) {
	labID, ok := parseIDParam(c.Param("labID"))
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ID 无效"})
		return
	}
	if _, ok := ensureLabOwnedByTeacher(c, labID); !ok {
		return
	}
	rows, err := db.Query(`
SELECT r.ReportID, r.LabID, l.LabName, c.CourseName, r.SNO, s.SName,
       r.OriginalName, r.StoredName, r.SizeBytes, r.SubmittedAt, r.Score, ISNULL(r.Comment,''), r.GradedAt
FROM Report r
JOIN Student s ON s.SNO=r.SNO
JOIN LabProject l ON l.LabID=r.LabID
JOIN Course c ON c.CourseID=l.CourseID
WHERE r.LabID=@p1
ORDER BY r.SubmittedAt DESC`, labID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	var reports []Report
	for rows.Next() {
		var r Report
		if err := rows.Scan(&r.ID, &r.LabID, &r.LabName, &r.CourseName, &r.SNO, &r.StudentName, &r.OriginalName, &r.StoredName, &r.SizeBytes, &r.SubmittedAt, &r.Score, &r.Comment, &r.GradedAt); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		reports = append(reports, r)
	}
	c.JSON(http.StatusOK, reports)
}

// downloadLabReportsHandler 把某实验项目的全部 PDF 打包成 ZIP。
func downloadLabReportsHandler(c *gin.Context) {
	labID, ok := parseIDParam(c.Param("labID"))
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ID 无效"})
		return
	}
	if _, ok := ensureLabOwnedByTeacher(c, labID); !ok {
		return
	}
	var labName string
	if err := db.QueryRow(`SELECT LabName FROM LabProject WHERE LabID=@p1`, labID).Scan(&labName); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	rows, err := db.Query(`SELECT StoredName, FilePath FROM Report WHERE LabID=@p1`, labID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	tmp, err := os.CreateTemp("", "reports-*.zip")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer os.Remove(tmp.Name())
	zw := zip.NewWriter(tmp)
	count := 0
	for rows.Next() {
		var name, path string
		if err := rows.Scan(&name, &path); err != nil {
			zw.Close()
			tmp.Close()
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if err := addFileToZip(zw, path, name); err != nil {
			zw.Close()
			tmp.Close()
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		count++
	}
	if err := zw.Close(); err != nil {
		tmp.Close()
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	tmp.Close()

	if ses := currentSession(c); ses != nil {
		audit(ses.UserID, "download_lab_zip", fmt.Sprintf("lab=%d count=%d", labID, count))
	}
	c.FileAttachment(tmp.Name(), fmt.Sprintf("%s-报告.zip", safeName(labName)))
}

// downloadSingleReportHandler 教师下载单个学生 PDF。
func downloadSingleReportHandler(c *gin.Context) {
	reportID, ok := parseIDParam(c.Param("reportID"))
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ID 无效"})
		return
	}
	var (
		storedName  string
		filePath    string
		ownerUserID int64
	)
	err := db.QueryRow(`
SELECT r.StoredName, r.FilePath, t.UserID
FROM Report r
JOIN LabProject l ON l.LabID=r.LabID
JOIN Course c ON c.CourseID=l.CourseID
JOIN Teacher t ON t.TeacherID=c.TeacherID
WHERE r.ReportID=@p1`, reportID).Scan(&storedName, &filePath, &ownerUserID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "报告不存在"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}
	ses := currentSession(c)
	if ses == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "未登录"})
		return
	}
	if ses.Role != "admin" && ses.UserID != ownerUserID {
		c.JSON(http.StatusForbidden, gin.H{"error": "无权下载"})
		return
	}
	c.FileAttachment(filePath, storedName)
}

// gradeReportHandler 教师录入/修改成绩与评语。
func gradeReportHandler(c *gin.Context) {
	reportID, ok := parseIDParam(c.Param("reportID"))
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ID 无效"})
		return
	}
	var body struct {
		Score   *float64 `json:"score"`
		Comment string   `json:"comment"`
	}
	if err := c.BindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体格式错误"})
		return
	}
	if body.Score != nil && (*body.Score < 0 || *body.Score > 100) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "成绩必须在 0~100"})
		return
	}

	// 校验归属
	var ownerUserID int64
	if err := db.QueryRow(`
SELECT t.UserID FROM Report r
JOIN LabProject l ON l.LabID=r.LabID
JOIN Course c ON c.CourseID=l.CourseID
JOIN Teacher t ON t.TeacherID=c.TeacherID
WHERE r.ReportID=@p1`, reportID).Scan(&ownerUserID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "报告不存在"})
		return
	}
	ses := currentSession(c)
	if ses == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "未登录"})
		return
	}
	if ses.Role != "admin" && ses.UserID != ownerUserID {
		c.JSON(http.StatusForbidden, gin.H{"error": "无权评分"})
		return
	}

	if _, err := db.Exec(`
UPDATE Report SET Score=@p1, Comment=@p2, GradedAt=SYSUTCDATETIME(), GradedBy=@p3 WHERE ReportID=@p4`,
		body.Score, strings.TrimSpace(body.Comment), ses.UserID, reportID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	audit(ses.UserID, "grade_report", fmt.Sprintf("report=%d", reportID))
	c.JSON(http.StatusOK, gin.H{"message": "评分已保存"})
}

// exportCourseGradesHandler 把课程下所有学生在每个实验项目的成绩与评语
// 导出为 Excel：列顺序 ID / SNO / Sname / Lab1 成绩 / Lab1 评语 / Lab2 成绩 / Lab2 评语 / ...
func exportCourseGradesHandler(c *gin.Context) {
	courseID, ok := parseIDParam(c.Param("courseID"))
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ID 无效"})
		return
	}
	if !ensureCourseOwnedByTeacher(c, courseID) {
		return
	}

	var courseName, semester string
	if err := db.QueryRow(`SELECT CourseName, Semester FROM Course WHERE CourseID=@p1`, courseID).Scan(&courseName, &semester); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "课程不存在"})
		return
	}

	// 1) 实验项目（按 LabID 升序，列序号稳定）
	labRows, err := db.Query(`SELECT LabID, LabName FROM LabProject WHERE CourseID=@p1 ORDER BY LabID`, courseID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	type labCol struct {
		id   int64
		name string
	}
	var labs []labCol
	for labRows.Next() {
		var l labCol
		if err := labRows.Scan(&l.id, &l.name); err != nil {
			labRows.Close()
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		labs = append(labs, l)
	}
	labRows.Close()

	// 2) 学生（按 SNO 升序）
	stuRows, err := db.Query(`
SELECT s.SNO, s.SName FROM Student s
JOIN CourseStudent cs ON cs.SNO=s.SNO
WHERE cs.CourseID=@p1
ORDER BY s.SNO`, courseID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	type stuRow struct {
		sno, name string
	}
	var students []stuRow
	for stuRows.Next() {
		var s stuRow
		if err := stuRows.Scan(&s.sno, &s.name); err != nil {
			stuRows.Close()
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		students = append(students, s)
	}
	stuRows.Close()

	// 3) 报告（一次性查全部，构造 (sno, labID) -> {score, comment} 映射）
	type cell struct {
		score   *float64
		comment string
		graded  bool
	}
	grades := map[string]cell{} // key = sno+"|"+labID
	rep, err := db.Query(`
SELECT r.SNO, r.LabID, r.Score, ISNULL(r.Comment,''), r.GradedAt
FROM Report r JOIN LabProject l ON l.LabID=r.LabID
WHERE l.CourseID=@p1`, courseID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	for rep.Next() {
		var (
			sno      string
			labID    int64
			score    sql.NullFloat64
			comment  string
			gradedAt sql.NullTime
		)
		if err := rep.Scan(&sno, &labID, &score, &comment, &gradedAt); err != nil {
			rep.Close()
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		key := fmt.Sprintf("%s|%d", sno, labID)
		ce := cell{comment: comment, graded: gradedAt.Valid}
		if score.Valid {
			v := score.Float64
			ce.score = &v
		}
		grades[key] = ce
	}
	rep.Close()

	// 4) 构造 xlsx
	f := excelize.NewFile()
	defer f.Close()
	sheet := "成绩"
	if _, err := f.NewSheet(sheet); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	f.DeleteSheet("Sheet1")

	// 表头
	headers := []any{"ID", "SNO", "Sname"}
	for _, l := range labs {
		headers = append(headers, l.name)
		headers = append(headers, l.name+" 评语")
	}
	if err := f.SetSheetRow(sheet, "A1", &headers); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	headStyle, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true},
		Fill:      excelize.Fill{Type: "pattern", Color: []string{"#E4F2EB"}, Pattern: 1},
		Alignment: &excelize.Alignment{Vertical: "center", Horizontal: "center"},
	})
	lastColIdx := 3 + len(labs)*2
	lastCol, _ := excelize.ColumnNumberToName(lastColIdx)
	_ = f.SetCellStyle(sheet, "A1", lastCol+"1", headStyle)

	// 数据行
	for i, s := range students {
		rowIdx := i + 2
		row := []any{i + 1, s.sno, s.name}
		for _, l := range labs {
			ce, ok := grades[fmt.Sprintf("%s|%d", s.sno, l.id)]
			if !ok {
				row = append(row, "未提交", "")
				continue
			}
			if ce.score != nil {
				row = append(row, *ce.score)
			} else {
				row = append(row, "未评分")
			}
			row = append(row, ce.comment)
		}
		if err := f.SetSheetRow(sheet, fmt.Sprintf("A%d", rowIdx), &row); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}

	// 列宽
	_ = f.SetColWidth(sheet, "A", "A", 6)
	_ = f.SetColWidth(sheet, "B", "B", 14)
	_ = f.SetColWidth(sheet, "C", "C", 12)
	if len(labs) > 0 {
		startCol, _ := excelize.ColumnNumberToName(4)
		endCol, _ := excelize.ColumnNumberToName(lastColIdx)
		_ = f.SetColWidth(sheet, startCol, endCol, 22)
	}

	if ses := currentSession(c); ses != nil {
		audit(ses.UserID, "export_grades", fmt.Sprintf("course=%d students=%d labs=%d", courseID, len(students), len(labs)))
	}

	filename := fmt.Sprintf("%s_%s_成绩.xlsx", safeName(courseName), safeName(semester))
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="grades.xlsx"; filename*=UTF-8''%s`, urlEscape(filename)))
	c.Header("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	if err := f.Write(c.Writer); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
}
