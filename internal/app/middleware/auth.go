package middleware

import (
	"database/sql"
	"net/http"

	"github.com/gin-gonic/gin"

	"wikios/internal/config"
	"wikios/internal/store"
)

const adminUserContextKey = "admin_user"

func AdminSessionAuth(cfg config.AuthConfig, dataStore *store.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, err := c.Cookie(cfg.SessionCookieName)
		if err != nil || token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": gin.H{
					"code":    "UNAUTHORIZED",
					"message": "admin login required",
				},
			})
			return
		}
		user, err := dataStore.GetSessionUser(c.Request.Context(), token)
		if err != nil {
			if err == sql.ErrNoRows {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
					"error": gin.H{
						"code":    "UNAUTHORIZED",
						"message": "session expired or invalid",
					},
				})
				return
			}
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"error": gin.H{
					"code":    "INTERNAL_ERROR",
					"message": err.Error(),
				},
			})
			return
		}
		c.Set(adminUserContextKey, user)
		c.Next()
	}
}

func AdminUser(c *gin.Context) (*store.AdminUser, bool) {
	value, ok := c.Get(adminUserContextKey)
	if !ok {
		return nil, false
	}
	user, ok := value.(*store.AdminUser)
	return user, ok
}
