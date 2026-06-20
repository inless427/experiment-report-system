package main

import (
	"log"
	"os"

	"github.com/gin-gonic/gin"
)

func main() {
	var err error
	db, err = openDB()
	if err != nil {
		log.Fatalf("连接数据库失败: %v", err)
	}
	defer db.Close()

	if err := migrate(db); err != nil {
		log.Fatalf("迁移数据库失败: %v", err)
	}

	if err := os.MkdirAll(uploadRoot, 0o755); err != nil {
		log.Fatalf("创建上传目录失败: %v", err)
	}

	if envStr("GIN_MODE", "") == "" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.Default()
	r.MaxMultipartMemory = maxExcelSize

	// 静态前端资源
	r.Static("/assets", "./web/assets")
	r.StaticFile("/", "./web/index.html")
	r.StaticFile("/index.html", "./web/index.html")

	registerRoutes(r)

	addr := envStr("APP_ADDR", ":8080")
	log.Printf("实验报告管理系统已启动: http://localhost%s", addr)
	if err := r.Run(addr); err != nil {
		log.Fatal(err)
	}
}

func registerRoutes(r *gin.Engine) {
	api := r.Group("/api")
	api.GET("/health", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	// 认证
	api.POST("/auth/login", loginHandler)
	api.POST("/auth/logout", logoutHandler)
	api.GET("/auth/me", meHandler)
	api.POST("/auth/password", requireAuth, changePasswordHandler)

	// DBA
	admin := api.Group("/admin", requireRole("admin"))
	admin.GET("/stats", adminDashboardHandler)
	admin.GET("/users", adminListUsersHandler)
	admin.POST("/users", adminCreateUserHandler)
	admin.PATCH("/users/:userID/password", adminResetPasswordHandler)
	admin.DELETE("/users/:userID", adminDeleteUserHandler)
	admin.GET("/audit", adminAuditHandler)
	admin.GET("/courses", adminListCoursesHandler)

	// 教师
	teacher := api.Group("/teacher", requireRole("teacher", "admin"))
	teacher.GET("/courses", teacherListCoursesHandler)
	teacher.POST("/courses/import", importCourseHandler)
	teacher.GET("/courses/:courseID/labs", listLabsHandler)
	teacher.GET("/courses/:courseID/students", listStudentsHandler)
	teacher.GET("/courses/:courseID/grades/export", exportCourseGradesHandler)
	teacher.PATCH("/labs/:labID", updateLabHandler)
	teacher.DELETE("/labs/:labID", deleteLabHandler)
	teacher.GET("/labs/:labID/reports", listReportsHandler)
	teacher.GET("/labs/:labID/reports/download", downloadLabReportsHandler)
	teacher.GET("/reports/:reportID/file", downloadSingleReportHandler)
	teacher.PATCH("/reports/:reportID/grade", gradeReportHandler)

	// 学生
	student := api.Group("/student", requireRole("student"))
	student.GET("/courses", studentMyCoursesHandler)
	student.GET("/courses/:courseID/labs", studentLabsHandler)
	student.GET("/labs/:labID/report", studentMyReportHandler)
	student.POST("/labs/:labID/report", uploadReportHandler)
	student.GET("/labs/:labID/report/file", studentDownloadOwnHandler)
}
