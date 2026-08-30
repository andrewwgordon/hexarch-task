// csrf.go implements CSRF protection for the web UI (spec docs/auth.md
// NFR-7, phase 10 of docs/auth-plan.md): a random token in a cookie
// (hexarch_csrf) that must be echoed in a hidden form field (csrf_token) or
// the X-CSRF-Token header on every state-changing request (POST/PATCH/DELETE).
// SameSite=Lax alone is not sufficient for same-site HTMX mutations.
//
// Public API: (none — csrf helpers are unexported)
//
// Private:
//   - csrfCookieName, ensureCSRFToken, verifyCSRF, randomToken
package httpweb

import (
	"crypto/hmac"
	"crypto/rand"
	"encoding/hex"
	"net/http"

	"github.com/gin-gonic/gin"
)

const csrfCookieName = "hexarch_csrf"

// randomToken returns a 256-bit random hex token.
func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("httpweb: cannot generate csrf token: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// ensureCSRFToken returns the CSRF token for this browser, setting the
// cookie when absent. Call it while rendering any page containing forms.
func ensureCSRFToken(c *gin.Context) string {
	if tok, err := c.Cookie(csrfCookieName); err == nil && tok != "" {
		return tok
	}
	tok := randomToken()
	c.SetCookie(csrfCookieName, tok, int(sessionTTL.Seconds()), "/", "", secureCookie(c), true)
	return tok
}

// verifyCSRF aborts with 403 unless the request carries the token that
// matches the browser's csrf cookie (form field csrf_token or the
// X-CSRF-Token header, compared in constant time). Returns true when valid.
func verifyCSRF(c *gin.Context) bool {
	cookieTok, err := c.Cookie(csrfCookieName)
	if err != nil || cookieTok == "" {
		c.AbortWithStatus(http.StatusForbidden)
		return false
	}
	tok := c.PostForm("csrf_token")
	if tok == "" {
		tok = c.GetHeader("X-CSRF-Token")
	}
	if !hmac.Equal([]byte(tok), []byte(cookieTok)) || tok == "" {
		c.AbortWithStatus(http.StatusForbidden)
		return false
	}
	return true
}

// secureCookie sets the Secure attribute when the request arrived over TLS.
func secureCookie(c *gin.Context) bool { return c.Request.TLS != nil }

// csrfProtect is a Gin middleware that enforces verifyCSRF on every
// state-changing method (POST/PATCH/DELETE); GET/HEAD/OPTIONS pass through.
func csrfProtect() gin.HandlerFunc {
	return func(c *gin.Context) {
		switch c.Request.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			c.Next()
			return
		}
		if !verifyCSRF(c) {
			return
		}
		c.Next()
	}
}
