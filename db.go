package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"

	_ "github.com/microsoft/go-mssqldb"
	"golang.org/x/crypto/bcrypt"
)

var db *sql.DB

func openDB() (*sql.DB, error) {
	conn := envStr("DB_CONN", `server=localhost;database=DB;encrypt=disable`)
	d, err := sql.Open("sqlserver", conn)
	if err != nil {
		return nil, err
	}
	d.SetMaxOpenConns(20)
	d.SetMaxIdleConns(5)
	d.SetConnMaxLifetime(time.Hour)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := d.PingContext(ctx); err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}

// dropLegacyTables 检测旧版 lab-report-system 留下的同名表，结构不兼容则清理。
// 判定依据：Course 表存在但缺少 TeacherID 列（老版本叫 TeacherName）。
func dropLegacyTables(d *sql.DB) error {
	var legacy int
	err := d.QueryRow(`
SELECT COUNT(*) FROM sys.tables t
WHERE t.name='Course'
  AND NOT EXISTS (SELECT 1 FROM sys.columns c WHERE c.object_id=t.object_id AND c.name='TeacherID')`).Scan(&legacy)
	if err != nil {
		return err
	}
	if legacy == 0 {
		return nil
	}
	log.Println("检测到旧版 lab-report-system 的同名表（不含 TeacherID），将清理后重建。")
	// 按外键依赖反序删除
	for _, t := range []string{"Report", "CourseStudent", "LabProject", "Student", "Course"} {
		if _, err := d.Exec(fmt.Sprintf("IF OBJECT_ID('dbo.%s','U') IS NOT NULL DROP TABLE dbo.%s", t, t)); err != nil {
			return fmt.Errorf("清理旧表 %s 失败: %w", t, err)
		}
	}
	return nil
}

// migrate 自动建库建表，幂等执行。
func migrate(d *sql.DB) error {
	if err := dropLegacyTables(d); err != nil {
		return err
	}
	stmts := []string{
		`IF NOT EXISTS (SELECT 1 FROM sys.tables WHERE name='AppUser')
CREATE TABLE AppUser (
    UserID INT IDENTITY(1,1) PRIMARY KEY,
    Username VARCHAR(64) NOT NULL UNIQUE,
    PasswordHash VARCHAR(128) NOT NULL,
    Role VARCHAR(16) NOT NULL,
    DisplayName NVARCHAR(50) NOT NULL,
    MustChangePassword BIT NOT NULL DEFAULT 1,
    CreatedAt DATETIME2 NOT NULL DEFAULT SYSUTCDATETIME(),
    CONSTRAINT CK_AppUser_Role CHECK (Role IN ('admin','teacher','student'))
)`,
		// 老版本表升级：补充 MustChangePassword 列
		`IF NOT EXISTS (SELECT 1 FROM sys.columns c
JOIN sys.tables t ON c.object_id=t.object_id
WHERE t.name='AppUser' AND c.name='MustChangePassword')
ALTER TABLE AppUser ADD MustChangePassword BIT NOT NULL DEFAULT 1`,
		`IF NOT EXISTS (SELECT 1 FROM sys.tables WHERE name='Teacher')
CREATE TABLE Teacher (
    TeacherID INT IDENTITY(1,1) PRIMARY KEY,
    UserID INT NOT NULL UNIQUE,
    Department NVARCHAR(80) NULL,
    Phone VARCHAR(20) NULL,
    CONSTRAINT FK_Teacher_AppUser FOREIGN KEY (UserID) REFERENCES AppUser(UserID) ON DELETE CASCADE
)`,
		`IF NOT EXISTS (SELECT 1 FROM sys.tables WHERE name='Student')
CREATE TABLE Student (
    SNO VARCHAR(32) PRIMARY KEY,
    UserID INT NOT NULL UNIQUE,
    SName NVARCHAR(50) NOT NULL,
    ClassName NVARCHAR(50) NULL,
    CONSTRAINT FK_Student_AppUser FOREIGN KEY (UserID) REFERENCES AppUser(UserID) ON DELETE CASCADE
)`,
		`IF NOT EXISTS (SELECT 1 FROM sys.tables WHERE name='Course')
CREATE TABLE Course (
    CourseID INT IDENTITY(1,1) PRIMARY KEY,
    CourseName NVARCHAR(100) NOT NULL,
    TeacherID INT NOT NULL,
    Semester VARCHAR(20) NOT NULL DEFAULT '2025-2026-2',
    FolderName NVARCHAR(160) NOT NULL UNIQUE,
    CreatedAt DATETIME2 NOT NULL DEFAULT SYSUTCDATETIME(),
    CONSTRAINT FK_Course_Teacher FOREIGN KEY (TeacherID) REFERENCES Teacher(TeacherID),
    CONSTRAINT UQ_Course_NameSemester UNIQUE (CourseName, Semester)
)`,
		`IF NOT EXISTS (SELECT 1 FROM sys.tables WHERE name='LabProject')
CREATE TABLE LabProject (
    LabID INT IDENTITY(1,1) PRIMARY KEY,
    CourseID INT NOT NULL,
    LabName NVARCHAR(120) NOT NULL,
    Description NVARCHAR(500) NULL,
    Status VARCHAR(16) NOT NULL DEFAULT 'closed',
    OpenAt DATETIME2 NULL,
    Deadline DATETIME2 NULL,
    FolderName NVARCHAR(160) NOT NULL,
    CreatedAt DATETIME2 NOT NULL DEFAULT SYSUTCDATETIME(),
    CONSTRAINT FK_LabProject_Course FOREIGN KEY (CourseID) REFERENCES Course(CourseID) ON DELETE CASCADE,
    CONSTRAINT UQ_LabProject_CourseName UNIQUE (CourseID, LabName),
    CONSTRAINT CK_LabProject_Status CHECK (Status IN ('open','closed','ended'))
)`,
		`IF NOT EXISTS (SELECT 1 FROM sys.tables WHERE name='CourseStudent')
CREATE TABLE CourseStudent (
    CourseID INT NOT NULL,
    SNO VARCHAR(32) NOT NULL,
    EnrolledAt DATETIME2 NOT NULL DEFAULT SYSUTCDATETIME(),
    PRIMARY KEY (CourseID, SNO),
    CONSTRAINT FK_CourseStudent_Course FOREIGN KEY (CourseID) REFERENCES Course(CourseID) ON DELETE CASCADE,
    CONSTRAINT FK_CourseStudent_Student FOREIGN KEY (SNO) REFERENCES Student(SNO)
)`,
		`IF NOT EXISTS (SELECT 1 FROM sys.tables WHERE name='Report')
CREATE TABLE Report (
    ReportID INT IDENTITY(1,1) PRIMARY KEY,
    LabID INT NOT NULL,
    SNO VARCHAR(32) NOT NULL,
    OriginalName NVARCHAR(260) NOT NULL,
    StoredName NVARCHAR(260) NOT NULL,
    FilePath NVARCHAR(500) NOT NULL,
    SizeBytes BIGINT NOT NULL,
    SubmittedAt DATETIME2 NOT NULL DEFAULT SYSUTCDATETIME(),
    Score DECIMAL(5,2) NULL,
    Comment NVARCHAR(500) NULL,
    GradedAt DATETIME2 NULL,
    GradedBy INT NULL,
    CONSTRAINT FK_Report_Lab FOREIGN KEY (LabID) REFERENCES LabProject(LabID) ON DELETE CASCADE,
    CONSTRAINT FK_Report_Student FOREIGN KEY (SNO) REFERENCES Student(SNO),
    CONSTRAINT FK_Report_Grader FOREIGN KEY (GradedBy) REFERENCES AppUser(UserID),
    CONSTRAINT UQ_Report_LabStudent UNIQUE (LabID, SNO),
    CONSTRAINT CK_Report_Score CHECK (Score IS NULL OR (Score >= 0 AND Score <= 100))
)`,
		`IF NOT EXISTS (SELECT 1 FROM sys.tables WHERE name='AuditLog')
CREATE TABLE AuditLog (
    LogID BIGINT IDENTITY(1,1) PRIMARY KEY,
    UserID INT NULL,
    Action VARCHAR(64) NOT NULL,
    Detail NVARCHAR(500) NULL,
    CreatedAt DATETIME2 NOT NULL DEFAULT SYSUTCDATETIME()
)`,
		`IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name='IX_LabProject_CourseStatus')
CREATE INDEX IX_LabProject_CourseStatus ON LabProject(CourseID, Status)`,
		`IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name='IX_Report_LabSubmitted')
CREATE INDEX IX_Report_LabSubmitted ON Report(LabID, SubmittedAt DESC)`,
		`IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name='IX_Report_SNO')
CREATE INDEX IX_Report_SNO ON Report(SNO)`,
		`IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name='IX_Course_Teacher')
CREATE INDEX IX_Course_Teacher ON Course(TeacherID)`,
		`IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name='IX_AuditLog_Created')
CREATE INDEX IX_AuditLog_Created ON AuditLog(CreatedAt DESC)`,
		`IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name='IX_CourseStudent_SNO')
CREATE INDEX IX_CourseStudent_SNO ON CourseStudent(SNO)`,
	}
	for _, s := range stmts {
		if _, err := d.Exec(s); err != nil {
			return err
		}
	}
	return seedDefaults(d)
}

// seedDefaults 在首次启动时写入默认管理员和教师账户。
func seedDefaults(d *sql.DB) error {
	defaults := []struct {
		username, password, role, display, dept string
	}{
		{"admin", "admin123", "admin", "系统管理员", ""},
		{"teacher", "teacher123", "teacher", "蔡云鹭", "数学与信息科学学院"},
	}
	for _, u := range defaults {
		var exists int
		if err := d.QueryRow("SELECT COUNT(*) FROM AppUser WHERE Username=@p1", u.username).Scan(&exists); err != nil {
			return err
		}
		if exists > 0 {
			continue
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(u.password), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		var uid int64
		if err := d.QueryRow(`INSERT INTO AppUser (Username, PasswordHash, Role, DisplayName)
OUTPUT inserted.UserID VALUES (@p1, @p2, @p3, @p4)`,
			u.username, string(hash), u.role, u.display).Scan(&uid); err != nil {
			return err
		}
		if u.role == "teacher" {
			if _, err := d.Exec(`INSERT INTO Teacher (UserID, Department) VALUES (@p1, @p2)`,
				uid, u.dept); err != nil {
				return err
			}
		}
		log.Printf("已创建默认账户 %s / %s (角色 %s)", u.username, u.password, u.role)
	}
	return nil
}
