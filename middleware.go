package main

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// requireAuth 仅校验登录态，不阻止"必须改密"的用户访问（因为改密接口本身要走这里）。
func requireAuth(c *gin.Context) {
	ses := currentSession(c)
	if ses == nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "请先登录"})
		return
	}
	c.Set("session", ses)
	c.Next()
}

// requireRole 校验登录态 + 角色。如果当前用户还没修改初始密码，拦截除改密外的一切接口。
func requireRole(roles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		ses := currentSession(c)
		if ses == nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "请先登录"})
			return
		}
		if ses.MustChange {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error":              "请先修改初始密码",
				"mustChangePassword": true,
			})
			return
		}
		for _, r := range roles {
			if ses.Role == r {
				c.Set("session", ses)
				c.Next()
				return
			}
		}
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "无权访问"})
	}
}
