package main

import (
	"time"
)

type AppUser struct {
	ID          int64     `json:"id"`
	Username    string    `json:"username"`
	Role        string    `json:"role"`
	DisplayName string    `json:"displayName"`
	CreatedAt   time.Time `json:"createdAt"`
}

type Course struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	TeacherID  int64     `json:"teacherId"`
	Teacher    string    `json:"teacher"`
	Semester   string    `json:"semester"`
	FolderName string    `json:"folderName"`
	CreatedAt  time.Time `json:"createdAt"`
	Students   int       `json:"studentCount,omitempty"`
	Labs       int       `json:"labCount,omitempty"`
}

type Lab struct {
	ID          int64      `json:"id"`
	CourseID    int64      `json:"courseId"`
	CourseName  string     `json:"courseName,omitempty"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Status      string     `json:"status"`
	OpenAt      *time.Time `json:"openAt"`
	Deadline    *time.Time `json:"deadline"`
	FolderName  string     `json:"folderName"`
	CreatedAt   time.Time  `json:"createdAt"`
	StudentCnt  int        `json:"studentCount"`
	Submitted   int        `json:"submittedCount"`
	// 仅用于学生视图：当前学生在本实验项目上的提交与评分情况
	MyScore       *float64   `json:"myScore,omitempty"`
	MyComment     string     `json:"myComment,omitempty"`
	MyGradedAt    *time.Time `json:"myGradedAt,omitempty"`
	MySubmittedAt *time.Time `json:"mySubmittedAt,omitempty"`
}

type Student struct {
	SNO       string `json:"sno"`
	Name      string `json:"name"`
	ClassName string `json:"className"`
}

type Report struct {
	ID           int64      `json:"id"`
	LabID        int64      `json:"labId"`
	LabName      string     `json:"labName,omitempty"`
	CourseName   string     `json:"courseName,omitempty"`
	SNO          string     `json:"sno"`
	StudentName  string     `json:"studentName"`
	OriginalName string     `json:"originalName"`
	StoredName   string     `json:"storedName"`
	SizeBytes    int64      `json:"sizeBytes"`
	SubmittedAt  time.Time  `json:"submittedAt"`
	Score        *float64   `json:"score"`
	Comment      string     `json:"comment"`
	GradedAt     *time.Time `json:"gradedAt"`
}

type AuditEntry struct {
	ID        int64     `json:"id"`
	Username  string    `json:"username"`
	Action    string    `json:"action"`
	Detail    string    `json:"detail"`
	CreatedAt time.Time `json:"createdAt"`
}

type Session struct {
	Token      string
	UserID     int64
	Username   string
	Role       string
	Display    string
	MustChange bool
	CreatedAt  time.Time
	ExpireAt   time.Time
}
