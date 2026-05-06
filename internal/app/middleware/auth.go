package middleware

import (
	"database/sql"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"wikios/internal/config"
	"wikios/internal/store"
)

const adminUserContextKey = "admin_user"
const adminSessionTokenContextKey = "admin_session_token"

func AdminSessionAuth(cfg config.AuthConfig, dataStore *store.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		tokens := RequestAdminSessionTokens(c, cfg.SessionCookieName)
		if len(tokens) == 0 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": gin.H{
					"code":    "UNAUTHORIZED",
					"message": "admin login required",
				},
			})
			return
		}

		for _, token := range tokens {
			user, err := dataStore.GetSessionUser(c.Request.Context(), token)
			if err == nil {
				c.Set(adminUserContextKey, user)
				c.Set(adminSessionTokenContextKey, token)
				c.Next()
				return
			}
			if err == sql.ErrNoRows {
				continue
			}
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"error": gin.H{
					"code":    "INTERNAL_ERROR",
					"message": err.Error(),
				},
			})
			return
		}

		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"error": gin.H{
				"code":    "UNAUTHORIZED",
				"message": "session expired or invalid",
			},
		})
	}
}

func RequestAdminSessionTokens(c *gin.Context, cookieName string) []string {
	tokens := make([]string, 0, 3)
	seen := map[string]struct{}{}
	add := func(token string) {
		token = strings.TrimSpace(token)
		if token == "" {
			return
		}
		if _, ok := seen[token]; ok {
			return
		}
		seen[token] = struct{}{}
		tokens = append(tokens, token)
	}

	if cookieName != "" {
		if token, err := c.Cookie(cookieName); err == nil {
			add(token)
		}
	}

	fields := strings.Fields(c.GetHeader("Authorization"))
	if len(fields) == 2 && strings.EqualFold(fields[0], "Bearer") {
		add(fields[1])
	}
	add(c.GetHeader("X-WikiOS-Admin-Session"))

	return tokens
}

func AdminUser(c *gin.Context) (*store.AdminUser, bool) {
	value, ok := c.Get(adminUserContextKey)
	if !ok {
		return nil, false
	}
	user, ok := value.(*store.AdminUser)
	return user, ok
}

func AuthenticatedAdminSessionToken(c *gin.Context) (string, bool) {
	value, ok := c.Get(adminSessionTokenContextKey)
	if !ok {
		return "", false
	}
	token, ok := value.(string)
	return token, ok && token != ""
}
