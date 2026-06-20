package main

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/xuri/excelize/v2"
	"golang.org/x/crypto/bcrypt"
)

// ImportSummary 导入摘要，回传给前端展示。
type ImportSummary struct {
	CourseID    int64    `json:"courseId"`
	CourseName  string   `json:"courseName"`
	Semester    string   `json:"semester"`
	FolderName  string   `json:"folderName"`
	TeacherName string   `json:"teacher"`
	Students    int      `json:"studentCount"`
	NewStudents int      `json:"newStudents"`
	Labs        int      `json:"labCount"`
	LabNames    []string `json:"labs"`
}

// importExcelCourse 解析教师上传的实验课信息 Excel 并入库。
// teacherUserID 用于：(1) 写审计；(2) 找到对应 TeacherID 作为课程负责人。
func importExcelCourse(path, courseName, semester string, teacherUserID int64) (*ImportSummary, error) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil, fmt.Errorf("打开 Excel 失败: %w", err)
	}
	defer f.Close()

	sheet := f.GetSheetName(0)
	rows, err := f.GetRows(sheet)
	if err != nil {
		return nil, fmt.Errorf("读取 Excel 失败: %w", err)
	}
	if len(rows) < 2 {
		return nil, errors.New("Excel 至少需要表头和一行数据")
	}
	if len(rows[0]) < 4 {
		return nil, errors.New("Excel 至少需要 ID、SNO、Sname 以及一个实验项目列")
	}

	headers := rows[0]
	labNames := cleanHeaders(headers[3:])
	if len(labNames) == 0 {
		return nil, errors.New("未识别到实验项目列（第 4 列起的表头不能为空）")
	}

	courseName = strings.TrimSpace(courseName)
	if courseName == "" {
		courseName = "数据库原理与应用实验"
	}
	semester = strings.TrimSpace(semester)
	if semester == "" {
		semester = "2025-2026-2"
	}

	// 解析教师信息（用于把课程挂到当前登录教师下）
	var (
		teacherID    int64
		teacherName  string
		teacherDept  sql.NullString
	)
	err = db.QueryRow(`
SELECT t.TeacherID, a.DisplayName, ISNULL(t.Department,'')
FROM Teacher t JOIN AppUser a ON a.UserID=t.UserID
WHERE t.UserID=@p1`, teacherUserID).Scan(&teacherID, &teacherName, &teacherDept)
	if err != nil {
		return nil, fmt.Errorf("当前登录用户不是有效教师: %w", err)
	}

	folder := safeName(courseName + "_" + semester)

	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// 1. 课程：存在则复用，不存在则新增
	var courseID int64
	if err := tx.QueryRow(`
SELECT CourseID FROM Course WHERE CourseName=@p1 AND Semester=@p2`, courseName, semester).Scan(&courseID); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("查询课程失败: %w", err)
		}
		if err := tx.QueryRow(`
INSERT INTO Course (CourseName, TeacherID, Semester, FolderName)
OUTPUT inserted.CourseID
VALUES (@p1, @p2, @p3, @p4)`, courseName, teacherID, semester, folder).Scan(&courseID); err != nil {
			return nil, fmt.Errorf("创建课程失败: %w", err)
		}
	} else {
		// 已存在：更新负责教师
		if _, err := tx.Exec(`UPDATE Course SET TeacherID=@p1 WHERE CourseID=@p2`, teacherID, courseID); err != nil {
			return nil, fmt.Errorf("更新课程教师失败: %w", err)
		}
	}

	// 取实际 FolderName（已存在课程时使用原 FolderName）
	if err := tx.QueryRow(`SELECT FolderName FROM Course WHERE CourseID=@p1`, courseID).Scan(&folder); err != nil {
		return nil, err
	}

	// 2. 学生与选课
	newStudents := 0
	studentTotal := 0
	for i := 1; i < len(rows); i++ {
		row := rows[i]
		sno := strings.TrimSpace(cell(row, 1))
		if sno == "" {
			continue
		}
		sname := strings.TrimSpace(cell(row, 2))
		if sname == "" {
			sname = "未命名"
		}

		// 2.1 AppUser - 不存在则创建（默认密码 = 学号）
		var userID int64
		err := tx.QueryRow(`SELECT UserID FROM AppUser WHERE Username=@p1`, sno).Scan(&userID)
		if errors.Is(err, sql.ErrNoRows) {
			hash, herr := bcrypt.GenerateFromPassword([]byte(sno), bcrypt.DefaultCost)
			if herr != nil {
				return nil, fmt.Errorf("生成密码哈希失败: %w", herr)
			}
			if err := tx.QueryRow(`
INSERT INTO AppUser (Username, PasswordHash, Role, DisplayName)
OUTPUT inserted.UserID
VALUES (@p1, @p2, 'student', @p3)`, sno, string(hash), sname).Scan(&userID); err != nil {
				return nil, fmt.Errorf("创建学生账户 %s 失败: %w", sno, err)
			}
			newStudents++
		} else if err != nil {
			return nil, err
		}

		// 2.2 Student - 不存在则创建
		var existingSno string
		if err := tx.QueryRow(`SELECT SNO FROM Student WHERE SNO=@p1`, sno).Scan(&existingSno); errors.Is(err, sql.ErrNoRows) {
			if _, err := tx.Exec(`INSERT INTO Student (SNO, UserID, SName) VALUES (@p1, @p2, @p3)`, sno, userID, sname); err != nil {
				return nil, fmt.Errorf("插入学生 %s 失败: %w", sno, err)
			}
		} else if err == nil {
			// 已存在则更新姓名
			if _, err := tx.Exec(`UPDATE Student SET SName=@p1 WHERE SNO=@p2`, sname, sno); err != nil {
				return nil, err
			}
		} else {
			return nil, err
		}

		// 2.3 选课
		if _, err := tx.Exec(`
IF NOT EXISTS (SELECT 1 FROM CourseStudent WHERE CourseID=@p1 AND SNO=@p2)
INSERT INTO CourseStudent (CourseID, SNO) VALUES (@p1, @p2)`, courseID, sno); err != nil {
			return nil, fmt.Errorf("登记选课 %s 失败: %w", sno, err)
		}
		studentTotal++
	}

	// 3. 实验项目 + 落盘目录
	for _, lab := range labNames {
		labFolder := safeName(lab)
		if _, err := tx.Exec(`
IF NOT EXISTS (SELECT 1 FROM LabProject WHERE CourseID=@p1 AND LabName=@p2)
INSERT INTO LabProject (CourseID, LabName, Status, FolderName)
VALUES (@p1, @p2, 'closed', @p3)`, courseID, lab, labFolder); err != nil {
			return nil, fmt.Errorf("创建实验项目 %s 失败: %w", lab, err)
		}
		if err := os.MkdirAll(filepath.Join(uploadRoot, folder, labFolder), 0o755); err != nil {
			return nil, fmt.Errorf("创建目录失败: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return &ImportSummary{
		CourseID:    courseID,
		CourseName:  courseName,
		Semester:    semester,
		FolderName:  folder,
		TeacherName: teacherName,
		Students:    studentTotal,
		NewStudents: newStudents,
		Labs:        len(labNames),
		LabNames:    labNames,
	}, nil
}

func cleanHeaders(headers []string) []string {
	seen := map[string]bool{}
	var result []string
	for _, h := range headers {
		name := strings.TrimSpace(h)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		result = append(result, name)
	}
	return result
}

func cell(row []string, i int) string {
	if i >= len(row) {
		return ""
	}
	return row[i]
}
