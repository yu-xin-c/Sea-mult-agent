package api

import (
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	anonUserCookieName = "sa_uid"
	sessionCookieName  = "sa_sid"
	userIDHeaderName   = "X-User-Id"
	sessionHeaderName  = "X-Session-Id"
)

func ensureAnonUserIDCookie(c *gin.Context) string {
	if c == nil {
		return ""
	}
	if v, err := c.Cookie(anonUserCookieName); err == nil && strings.TrimSpace(v) != "" {
		return v
	}
	id := uuid.NewString()
	setHTTPOnlyCookie(c, anonUserCookieName, id, cookieMaxAgeSeconds())
	return id
}

func resolveUserID(c *gin.Context) string {
	if c == nil {
		return ""
	}
	if headerValue := sanitizeIdentityValue(c.GetHeader(userIDHeaderName)); headerValue != "" {
		setHTTPOnlyCookie(c, anonUserCookieName, headerValue, cookieMaxAgeSeconds())
		return headerValue
	}
	return ensureAnonUserIDCookie(c)
}

func ensureSessionIDCookie(c *gin.Context) string {
	if c == nil {
		return ""
	}
	if v, err := c.Cookie(sessionCookieName); err == nil && strings.TrimSpace(v) != "" {
		return v
	}
	id := uuid.NewString()
	setHTTPOnlyCookie(c, sessionCookieName, id, cookieMaxAgeSeconds())
	return id
}

func resolveSessionID(c *gin.Context) string {
	if c == nil {
		return ""
	}
	if headerValue := sanitizeIdentityValue(c.GetHeader(sessionHeaderName)); headerValue != "" {
		setHTTPOnlyCookie(c, sessionCookieName, headerValue, cookieMaxAgeSeconds())
		return headerValue
	}
	return ensureSessionIDCookie(c)
}

func cookieMaxAgeSeconds() int {
	const defaultMaxAge = int((7 * 24 * time.Hour) / time.Second)
	if v := strings.TrimSpace(os.Getenv("INTENT_SESSION_COOKIE_MAX_AGE_SECONDS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return defaultMaxAge
}

func setHTTPOnlyCookie(c *gin.Context, name, value string, maxAgeSeconds int) {
	if c == nil {
		return
	}
	// 本地开发默认 Lax，避免跨域 Cookie 场景下被浏览器直接丢弃。
	c.SetSameSite(parseSameSite(os.Getenv("INTENT_COOKIE_SAMESITE")))
	c.SetCookie(name, value, maxAgeSeconds, "/", cookieDomain(), cookieSecure(c), true)
}

func cookieDomain() string {
	return strings.TrimSpace(os.Getenv("INTENT_COOKIE_DOMAIN"))
}

func cookieSecure(c *gin.Context) bool {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("INTENT_COOKIE_SECURE")), "true") {
		return true
	}
	return c != nil && c.Request != nil && c.Request.TLS != nil
}

func parseSameSite(raw string) http.SameSite {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "none":
		return http.SameSiteNoneMode
	case "strict":
		return http.SameSiteStrictMode
	default:
		return http.SameSiteLaxMode
	}
}

func sanitizeIdentityValue(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	builder := strings.Builder{}
	builder.Grow(len(raw))
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z':
			builder.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			builder.WriteRune(r)
		case r >= '0' && r <= '9':
			builder.WriteRune(r)
		case r == '-' || r == '_' || r == '.' || r == '@':
			builder.WriteRune(r)
		}
		if builder.Len() >= 128 {
			break
		}
	}
	return builder.String()
}
