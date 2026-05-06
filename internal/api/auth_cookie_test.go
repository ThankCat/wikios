package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"wikios/internal/config"
)

func TestAdminSessionCookieCanUseIframeAttributes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	h := &Handlers{AuthConfig: config.AuthConfig{
		SessionCookieName:     "sid",
		SessionCookieDomain:   "admin.example.com",
		SessionCookieSecure:   true,
		SessionCookieSameSite: "none",
	}}

	h.setAdminSessionCookie(c, "token", 3600)

	header := rec.Header().Get("Set-Cookie")
	for _, want := range []string{"sid=token", "Domain=admin.example.com", "Path=/", "Max-Age=3600", "HttpOnly", "Secure", "SameSite=None"} {
		if !strings.Contains(header, want) {
			t.Fatalf("expected Set-Cookie to contain %q, got %q", want, header)
		}
	}
	if got := adminCookieSameSite("none"); got != http.SameSiteNoneMode {
		t.Fatalf("expected SameSite=None, got %v", got)
	}
}
