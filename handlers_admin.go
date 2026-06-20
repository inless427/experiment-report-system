package main

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

// adminDashboardHandler 返回 DBA 概览数据。
func adminDashboardHandler(c *gin.Context) {
	var result struct {
		Users      int `json:"users"`
		Teachers   int `json:"teachers"`
		Students   int `json:"students"`
		Courses    int `json:"courses"`
		Labs       int `json:"labs"`
		Reports    int `json:"reports"`
		OpenLabs   int `json:"openLabs"`
		ClosedLabs int `json:"closedLabs"`
		EndedLabs  int `json:"endedLabs"`
	}
	_ = db.QueryRow("SELECT COUNT(*) FROM AppUser").Scan(&result.Users)
	_ = db.QueryRow("SELECT COUNT(*) FROM Teacher").Scan(&result.Teachers)
	_ = db.QueryRow("SELECT COUNT(*) FROM Student").Scan(&result.Students)
	_ = db.QueryRow("SELECT COUNT(*) FROM Course").Scan(&result.Courses)
	_ = db.QueryRow("SELECT COUNT(*) FROM LabProject").Scan(&result.Labs)
	_ = db.QueryRow("SELECT COUNT(*) FROM Report").Scan(&result.Reports)
	_ = db.QueryRow("SELECT COUNT(*) FROM LabProject WHERE Status='open'").Scan(&result.OpenLabs)
	_ = db.QueryRow("SELECT COUNT(*) FROM LabProject WHERE Status='closed'").Scan(&result.ClosedLabs)
	_ = db.QueryRow("SELECT COUNT(*) FROM LabProject WHERE Status='ended'").Scan(&result.EndedLabs)
	c.JSON(http.StatusOK, result)
}

// adminListUsersHandler 列出所有用户。
func adminListUsersHandler(c *gin.Context) {
	rows, err := db.Query(`SELECT UserID, Username, Role, DisplayName, CreatedAt FROM AppUser ORDER BY UserID`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	var users []AppUser
	for rows.Next() {
		var u AppUser
		if err := rows.Scan(&u.ID, &u.Username, &u.Role, &u.DisplayName, &u.CreatedAt); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		users = append(users, u)
	}
	c.JSON(http.StatusOK, users)
}

// adminCreateUserHandler 创建新教师或管理员账户。
func adminCreateUserHandler(c *gin.Context) {
	var body struct {
		Username    string `json:"username"`
		Password    string `json:"password"`
		Role        string `json:"role"`
		DisplayName string `json:"displayName"`
		Department  string `json:"department"`
	}
	if err := c.BindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体格式错误"})
		return
	}
	body.Username = strings.TrimSpace(body.Username)
	body.DisplayName = strings.TrimSpace(body.DisplayName)
	if body.Username == "" || body.Password == "" || body.DisplayName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "用户名、密码和姓名都不能为空"})
		return
	}
	if body.Role != "admin" && body.Role != "teacher" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "通过此接口只能创建管理员或教师"})
		return
	}

	tx, err := db.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer tx.Rollback()

	hash, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	var uid int64
	err = tx.QueryRow(`INSERT INTO AppUser (Username, PasswordHash, Role, DisplayName)
OUTPUT inserted.UserID VALUES (@p1, @p2, @p3, @p4)`,
		body.Username, string(hash), body.Role, body.DisplayName).Scan(&uid)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "用户名已存在或参数无效: " + err.Error()})
		return
	}
	if body.Role == "teacher" {
		if _, err := tx.Exec(`INSERT INTO Teacher (UserID, Department) VALUES (@p1, @p2)`, uid, body.Department); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if ses := currentSession(c); ses != nil {
		audit(ses.UserID, "create_user", body.Username+" / "+body.Role)
	}
	c.JSON(http.StatusOK, gin.H{"id": uid, "message": "用户已创建"})
}

// adminResetPasswordHandler 由 DBA 重置任意账户密码。重置后该账户必须重新登录并立即改密。
func adminResetPasswordHandler(c *gin.Context) {
	uid, ok := parseIDParam(c.Param("userID"))
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ID 无效"})
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := c.BindJSON(&body); err != nil || strings.TrimSpace(body.Password) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "新密码不能为空"})
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if _, err := db.Exec(`UPDATE AppUser SET PasswordHash=@p1, MustChangePassword=1 WHERE UserID=@p2`,
		string(hash), uid); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// 踢掉该用户的所有现有会话，避免旧会话绕过强制改密
	sessions.dropByUserID(uid)
	if ses := currentSession(c); ses != nil {
		audit(ses.UserID, "reset_password", c.Param("userID"))
	}
	c.JSON(http.StatusOK, gin.H{"message": "密码已重置，用户下次登录后需立即修改"})
}

// adminDeleteUserHandler 删除账户（教师必须无关联课程，学生必须无报告）。
func adminDeleteUserHandler(c *gin.Context) {
	uid, ok := parseIDParam(c.Param("userID"))
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ID 无效"})
		return
	}
	ses := currentSession(c)
	if ses != nil && ses.UserID == uid {
		c.JSON(http.StatusBadRequest, gin.H{"error": "不能删除自己"})
		return
	}

	tx, err := db.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer tx.Rollback()

	var role string
	if err := tx.QueryRow(`SELECT Role FROM AppUser WHERE UserID=@p1`, uid).Scan(&role); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "用户不存在"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}

	if role == "teacher" {
		var cnt int
		_ = tx.QueryRow(`SELECT COUNT(*) FROM Course c JOIN Teacher t ON t.TeacherID=c.TeacherID WHERE t.UserID=@p1`, uid).Scan(&cnt)
		if cnt > 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "该教师仍负责课程，请先转让或删除课程"})
			return
		}
	}
	if role == "student" {
		var cnt int
		_ = tx.QueryRow(`SELECT COUNT(*) FROM Report r JOIN Student s ON s.SNO=r.SNO WHERE s.UserID=@p1`, uid).Scan(&cnt)
		if cnt > 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "该学生有已提交报告，请先删除报告"})
			return
		}
		if _, err := tx.Exec(`DELETE FROM CourseStudent WHERE SNO IN (SELECT SNO FROM Student WHERE UserID=@p1)`, uid); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	if _, err := tx.Exec(`DELETE FROM AppUser WHERE UserID=@p1`, uid); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if ses != nil {
		audit(ses.UserID, "delete_user", c.Param("userID"))
	}
	c.JSON(http.StatusOK, gin.H{"message": "已删除"})
}

// adminAuditHandler 返回最近 200 条审计记录。
func adminAuditHandler(c *gin.Context) {
	rows, err := db.Query(`SELECT TOP 200 a.LogID, ISNULL(u.Username,'') , a.Action, ISNULL(a.Detail,''), a.CreatedAt
FROM AuditLog a LEFT JOIN AppUser u ON u.UserID=a.UserID
ORDER BY a.LogID DESC`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	var entries []AuditEntry
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.Username, &e.Action, &e.Detail, &e.CreatedAt); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		entries = append(entries, e)
	}
	c.JSON(http.StatusOK, entries)
}

// adminListCoursesHandler 列出所有课程。
func adminListCoursesHandler(c *gin.Context) {
	rows, err := db.Query(`
SELECT c.CourseID, c.CourseName, t.TeacherID, a.DisplayName, c.Semester, c.FolderName, c.CreatedAt,
       (SELECT COUNT(*) FROM CourseStudent cs WHERE cs.CourseID=c.CourseID),
       (SELECT COUNT(*) FROM LabProject l WHERE l.CourseID=c.CourseID)
FROM Course c JOIN Teacher t ON t.TeacherID=c.TeacherID JOIN AppUser a ON a.UserID=t.UserID
ORDER BY c.CreatedAt DESC`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	var courses []Course
	for rows.Next() {
		var item Course
		if err := rows.Scan(&item.ID, &item.Name, &item.TeacherID, &item.Teacher, &item.Semester, &item.FolderName, &item.CreatedAt, &item.Students, &item.Labs); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		courses = append(courses, item)
	}
	c.JSON(http.StatusOK, courses)
}
