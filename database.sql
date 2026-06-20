/* ===========================================================
   实验报告管理系统 - 数据库脚本
   说明：本脚本满足 3NF，包含建库、建表、索引和默认数据。
   程序首次启动时会自动执行等价的迁移语句，无需手工运行。
   =========================================================== */

IF DB_ID(N'ExpReport') IS NULL
BEGIN
    CREATE DATABASE ExpReport;
END
GO

USE ExpReport;
GO

/* -----------------------------------------------------------
   1. AppUser  统一身份表
   每个用户都对应一个登录账号，角色由 Role 字段控制。
   MustChangePassword=1 表示首次登录或被管理员重置密码，必须改密后才能使用业务接口。
   ----------------------------------------------------------- */
IF NOT EXISTS (SELECT 1 FROM sys.tables WHERE name='AppUser')
CREATE TABLE AppUser (
    UserID             INT IDENTITY(1,1) PRIMARY KEY,
    Username           VARCHAR(64)  NOT NULL UNIQUE,
    PasswordHash       VARCHAR(128) NOT NULL,
    Role               VARCHAR(16)  NOT NULL,
    DisplayName        NVARCHAR(50) NOT NULL,
    MustChangePassword BIT          NOT NULL DEFAULT 1,
    CreatedAt          DATETIME2    NOT NULL DEFAULT SYSUTCDATETIME(),
    CONSTRAINT CK_AppUser_Role CHECK (Role IN ('admin','teacher','student'))
);
GO

-- 老版本升级：补齐 MustChangePassword 列
IF NOT EXISTS (SELECT 1 FROM sys.columns c
JOIN sys.tables t ON c.object_id=t.object_id
WHERE t.name='AppUser' AND c.name='MustChangePassword')
ALTER TABLE AppUser ADD MustChangePassword BIT NOT NULL DEFAULT 1;
GO

/* -----------------------------------------------------------
   2. Teacher  教师（与 AppUser 1:1，存放教师专属属性）
   ----------------------------------------------------------- */
IF NOT EXISTS (SELECT 1 FROM sys.tables WHERE name='Teacher')
CREATE TABLE Teacher (
    TeacherID  INT IDENTITY(1,1) PRIMARY KEY,
    UserID     INT          NOT NULL UNIQUE,
    Department NVARCHAR(80) NULL,
    Phone      VARCHAR(20)  NULL,
    CONSTRAINT FK_Teacher_AppUser FOREIGN KEY (UserID)
        REFERENCES AppUser(UserID) ON DELETE CASCADE
);
GO

/* -----------------------------------------------------------
   3. Student  学生
   学号作为主键，UserID 关联到登录账号。
   ----------------------------------------------------------- */
IF NOT EXISTS (SELECT 1 FROM sys.tables WHERE name='Student')
CREATE TABLE Student (
    SNO       VARCHAR(32) PRIMARY KEY,
    UserID    INT          NOT NULL UNIQUE,
    SName     NVARCHAR(50) NOT NULL,
    ClassName NVARCHAR(50) NULL,
    CONSTRAINT FK_Student_AppUser FOREIGN KEY (UserID)
        REFERENCES AppUser(UserID) ON DELETE CASCADE
);
GO

/* -----------------------------------------------------------
   4. Course  实验课程（属于某教师）
   FolderName 用于落盘的根目录名，全局唯一。
   ----------------------------------------------------------- */
IF NOT EXISTS (SELECT 1 FROM sys.tables WHERE name='Course')
CREATE TABLE Course (
    CourseID   INT IDENTITY(1,1) PRIMARY KEY,
    CourseName NVARCHAR(100) NOT NULL,
    TeacherID  INT           NOT NULL,
    Semester   VARCHAR(20)   NOT NULL DEFAULT '2025-2026-2',
    FolderName NVARCHAR(160) NOT NULL UNIQUE,
    CreatedAt  DATETIME2     NOT NULL DEFAULT SYSUTCDATETIME(),
    CONSTRAINT FK_Course_Teacher FOREIGN KEY (TeacherID) REFERENCES Teacher(TeacherID),
    CONSTRAINT UQ_Course_NameSemester UNIQUE (CourseName, Semester)
);
GO

/* -----------------------------------------------------------
   5. LabProject  实验项目（属于某课程）
   Status: open(开放) / closed(关闭) / ended(截止)
   ----------------------------------------------------------- */
IF NOT EXISTS (SELECT 1 FROM sys.tables WHERE name='LabProject')
CREATE TABLE LabProject (
    LabID       INT IDENTITY(1,1) PRIMARY KEY,
    CourseID    INT           NOT NULL,
    LabName     NVARCHAR(120) NOT NULL,
    Description NVARCHAR(500) NULL,
    Status      VARCHAR(16)   NOT NULL DEFAULT 'closed',
    OpenAt      DATETIME2     NULL,
    Deadline    DATETIME2     NULL,
    FolderName  NVARCHAR(160) NOT NULL,
    CreatedAt   DATETIME2     NOT NULL DEFAULT SYSUTCDATETIME(),
    CONSTRAINT FK_LabProject_Course FOREIGN KEY (CourseID)
        REFERENCES Course(CourseID) ON DELETE CASCADE,
    CONSTRAINT UQ_LabProject_CourseName UNIQUE (CourseID, LabName),
    CONSTRAINT CK_LabProject_Status CHECK (Status IN ('open','closed','ended'))
);
GO

/* -----------------------------------------------------------
   6. CourseStudent  选课关系（m:n）
   ----------------------------------------------------------- */
IF NOT EXISTS (SELECT 1 FROM sys.tables WHERE name='CourseStudent')
CREATE TABLE CourseStudent (
    CourseID   INT         NOT NULL,
    SNO        VARCHAR(32) NOT NULL,
    EnrolledAt DATETIME2   NOT NULL DEFAULT SYSUTCDATETIME(),
    PRIMARY KEY (CourseID, SNO),
    CONSTRAINT FK_CourseStudent_Course FOREIGN KEY (CourseID)
        REFERENCES Course(CourseID) ON DELETE CASCADE,
    CONSTRAINT FK_CourseStudent_Student FOREIGN KEY (SNO)
        REFERENCES Student(SNO)
);
GO

/* -----------------------------------------------------------
   7. Report  实验报告
   UQ_Report_LabStudent 保证每个实验项目每个学生只有一份报告。
   ----------------------------------------------------------- */
IF NOT EXISTS (SELECT 1 FROM sys.tables WHERE name='Report')
CREATE TABLE Report (
    ReportID     INT IDENTITY(1,1) PRIMARY KEY,
    LabID        INT          NOT NULL,
    SNO          VARCHAR(32)  NOT NULL,
    OriginalName NVARCHAR(260) NOT NULL,
    StoredName   NVARCHAR(260) NOT NULL,
    FilePath     NVARCHAR(500) NOT NULL,
    SizeBytes    BIGINT       NOT NULL,
    SubmittedAt  DATETIME2    NOT NULL DEFAULT SYSUTCDATETIME(),
    Score        DECIMAL(5,2) NULL,
    Comment      NVARCHAR(500) NULL,
    GradedAt     DATETIME2    NULL,
    GradedBy     INT          NULL,
    CONSTRAINT FK_Report_Lab FOREIGN KEY (LabID)
        REFERENCES LabProject(LabID) ON DELETE CASCADE,
    CONSTRAINT FK_Report_Student FOREIGN KEY (SNO)
        REFERENCES Student(SNO),
    CONSTRAINT FK_Report_Grader FOREIGN KEY (GradedBy)
        REFERENCES AppUser(UserID),
    CONSTRAINT UQ_Report_LabStudent UNIQUE (LabID, SNO),
    CONSTRAINT CK_Report_Score CHECK (Score IS NULL OR (Score >= 0 AND Score <= 100))
);
GO

/* -----------------------------------------------------------
   8. AuditLog  关键操作审计
   ----------------------------------------------------------- */
IF NOT EXISTS (SELECT 1 FROM sys.tables WHERE name='AuditLog')
CREATE TABLE AuditLog (
    LogID     BIGINT IDENTITY(1,1) PRIMARY KEY,
    UserID    INT           NULL,
    Action    VARCHAR(64)   NOT NULL,
    Detail    NVARCHAR(500) NULL,
    CreatedAt DATETIME2     NOT NULL DEFAULT SYSUTCDATETIME()
);
GO

/* -----------------------------------------------------------
   索引
   ----------------------------------------------------------- */
IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name='IX_LabProject_CourseStatus')
CREATE INDEX IX_LabProject_CourseStatus ON LabProject(CourseID, Status);
GO

IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name='IX_Report_LabSubmitted')
CREATE INDEX IX_Report_LabSubmitted ON Report(LabID, SubmittedAt DESC);
GO

IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name='IX_Report_SNO')
CREATE INDEX IX_Report_SNO ON Report(SNO);
GO

IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name='IX_Course_Teacher')
CREATE INDEX IX_Course_Teacher ON Course(TeacherID);
GO

IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name='IX_AuditLog_Created')
CREATE INDEX IX_AuditLog_Created ON AuditLog(CreatedAt DESC);
GO

-- 选课表反向查询：按学号查所属课程（学生登录拉取自己课程列表的高频查询）
IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name='IX_CourseStudent_SNO')
CREATE INDEX IX_CourseStudent_SNO ON CourseStudent(SNO);
GO

/* -----------------------------------------------------------
   默认账户（密码均使用 bcrypt 哈希，由程序首次启动时写入）
   admin   / admin123    : DBA
   teacher / teacher123  : 教师（默认指向 蔡云鹭）
   ----------------------------------------------------------- */
