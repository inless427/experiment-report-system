package main

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

const (
	sessionCookie = "exp_session"
	sessionTTL    = 8 * time.Hour
)

type sessionStore struct {
	mu       sync.RWMutex
	sessions map[string]*Session
}

var sessions = &sessionStore{sessions: map[string]*Session{}}

func (s *sessionStore) create(user *Session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[user.Token] = user
}

func (s *sessionStore) get(token string) *Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ses, ok := s.sessions[token]
	if !ok {
		return nil
	}
	if time.Now().After(ses.ExpireAt) {
		return nil
	}
	return ses
}

func (s *sessionStore) drop(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, token)
}

// dropByUserID 删除某用户的所有会话，配合管理员重置密码使用。
func (s *sessionStore) dropByUserID(userID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for tok, ses := range s.sessions {
		if ses.UserID == userID {
			delete(s.sessions, tok)
		}
	}
}

func newToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func loginHandler(c *gin.Context) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := c.BindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体格式错误"})
		return
	}
	username := strings.TrimSpace(body.Username)
	if username == "" || body.Password == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "用户名和密码不能为空"})
		return
	}

	var (
		userID     int64
		hash       string
		role       string
		display    string
		mustChange bool
	)
	err := db.QueryRow(`SELECT UserID, PasswordHash, Role, DisplayName, MustChangePassword FROM AppUser WHERE Username=@p1`,
		username).Scan(&userID, &hash, &role, &display, &mustChange)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "账号或密码错误"})
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(body.Password)); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "账号或密码错误"})
		return
	}

	ses := &Session{
		Token:      newToken(),
		UserID:     userID,
		Username:   username,
		Role:       role,
		Display:    display,
		MustChange: mustChange,
		CreatedAt:  time.Now(),
		ExpireAt:   time.Now().Add(sessionTTL),
	}
	sessions.create(ses)
	c.SetCookie(sessionCookie, ses.Token, int(sessionTTL.Seconds()), "/", "", false, true)
	audit(userID, "login", username)
	c.JSON(http.StatusOK, gin.H{
		"token": ses.Token,
		"user": gin.H{
			"id":                 userID,
			"username":           username,
			"role":               role,
			"displayName":        display,
			"mustChangePassword": mustChange,
		},
	})
}

func logoutHandler(c *gin.Context) {
	token, _ := c.Cookie(sessionCookie)
	if token == "" {
		token = readBearer(c)
	}
	if token != "" {
		if ses := sessions.get(token); ses != nil {
			audit(ses.UserID, "logout", ses.Username)
		}
		sessions.drop(token)
	}
	c.SetCookie(sessionCookie, "", -1, "/", "", false, true)
	c.JSON(http.StatusOK, gin.H{"message": "已退出"})
}

func meHandler(c *gin.Context) {
	ses := currentSession(c)
	if ses == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "未登录"})
		return
	}
	extra := gin.H{}
	if ses.Role == "student" {
		var sno, class string
		if err := db.QueryRow(`SELECT SNO, ISNULL(ClassName,'') FROM Student WHERE UserID=@p1`, ses.UserID).Scan(&sno, &class); err == nil {
			extra["sno"] = sno
			extra["className"] = class
		}
	}
	if ses.Role == "teacher" {
		var tid int64
		var dept string
		if err := db.QueryRow(`SELECT TeacherID, ISNULL(Department,'') FROM Teacher WHERE UserID=@p1`, ses.UserID).Scan(&tid, &dept); err == nil {
			extra["teacherId"] = tid
			extra["department"] = dept
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"id":                 ses.UserID,
		"username":           ses.Username,
		"role":               ses.Role,
		"displayName":        ses.Display,
		"mustChangePassword": ses.MustChange,
		"extra":              extra,
	})
}

func changePasswordHandler(c *gin.Context) {
	ses := currentSession(c)
	if ses == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "未登录"})
		return
	}
	var body struct {
		Old string `json:"oldPassword"`
		New string `json:"newPassword"`
	}
	if err := c.BindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体格式错误"})
		return
	}
	if len(body.New) < 6 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "新密码至少 6 位"})
		return
	}
	if body.New == body.Old {
		c.JSON(http.StatusBadRequest, gin.H{"error": "新密码不能与原密码相同"})
		return
	}
	var hash string
	if err := db.QueryRow(`SELECT PasswordHash FROM AppUser WHERE UserID=@p1`, ses.UserID).Scan(&hash); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "用户不存在"})
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(body.Old)); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "原密码错误"})
		return
	}
	newHash, err := bcrypt.GenerateFromPassword([]byte(body.New), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if _, err := db.Exec(`UPDATE AppUser SET PasswordHash=@p1, MustChangePassword=0 WHERE UserID=@p2`,
		string(newHash), ses.UserID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// 同步内存会话，避免下一次请求继续被强制改密拦截
	ses.MustChange = false
	audit(ses.UserID, "change_password", "")
	c.JSON(http.StatusOK, gin.H{"message": "密码已更新"})
}

func currentSession(c *gin.Context) *Session {
	if v, ok := c.Get("session"); ok {
		if ses, ok := v.(*Session); ok {
			return ses
		}
	}
	token, _ := c.Cookie(sessionCookie)
	if token == "" {
		token = readBearer(c)
	}
	if token == "" {
		return nil
	}
	return sessions.get(token)
}

func readBearer(c *gin.Context) string {
	h := c.GetHeader("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	return ""
}

// audit 写入审计日志，失败不会阻塞主流程。
func audit(userID int64, action, detail string) {
	if db == nil {
		return
	}
	if len(detail) > 480 {
		detail = detail[:480]
	}
	_, _ = db.Exec(`INSERT INTO AuditLog (UserID, Action, Detail) VALUES (@p1, @p2, @p3)`, userID, action, detail)
}
