// session.go implements the stateless signed session for the web UI
// (spec docs/auth.md §3.7): an HMAC-SHA256 cookie named hexarch_session
// carrying only the userid and expiry. Privileges (isadmin) are deliberately
// NOT embedded — middleware re-loads the user from the database on every
// request, so demotions/deletions take effect immediately (NFR-4, phase 10
// of docs/auth-plan.md).
//
// Secret resolution (final): HEXARCH_SESSION_SECRET when set; otherwise a
// random secret is generated per boot — all sessions are invalidated on
// restart, which is the documented trade-off.
//
// Public API: (none — session manager is unexported)
//
// Private:
//   - sessionManager, newSessionManager, issue, verify
//   - sessionCookieName, sessionTTL
package httpweb

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"hexarch/internal/domain"
)

const (
	sessionCookieName = "hexarch_session"
	sessionTTL        = 24 * time.Hour
)

// sessionManager signs and verifies session payloads with one key.
type sessionManager struct {
	secret []byte
}

// newSessionManager resolves the signing secret per the spec: env var or a
// fresh random per-boot value.
func newSessionManager() *sessionManager {
	if s := os.Getenv("HEXARCH_SESSION_SECRET"); s != "" {
		return &sessionManager{secret: []byte(s)}
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("httpweb: cannot generate session secret: " + err.Error())
	}
	return &sessionManager{secret: b}
}

// issue returns the cookie value for a user session valid for sessionTTL.
func (m *sessionManager) issue(userid domain.UserID, now time.Time) string {
	expiry := now.Add(sessionTTL).Unix()
	payload := fmt.Sprintf("%s|%d", userid.String(), expiry)
	return m.sign(payload)
}

// verify decodes and validates a cookie value (format: userid|expiry|sig),
// returning the userid and whether the session is valid (signature + not
// expired).
func (m *sessionManager) verify(value string, now time.Time) (domain.UserID, bool) {
	parts := strings.Split(value, "|")
	if len(parts) != 3 {
		return "", false
	}
	userid, expStr, sig := parts[0], parts[1], parts[2]
	if !m.validSig(userid+"|"+expStr, sig) {
		return "", false
	}
	expiry, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil || now.Unix() > expiry {
		return "", false
	}
	if userid == "" {
		return "", false
	}
	return domain.UserID(userid), true
}

// sign returns base64url(HMAC-SHA256(secret, payload)) + "|" + payload,
// i.e. the wire format "payload|sig".
func (m *sessionManager) sign(payload string) string {
	return payload + "|" + m.mac(payload)
}

func (m *sessionManager) mac(payload string) string {
	h := hmac.New(sha256.New, m.secret)
	h.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

func (m *sessionManager) validSig(payload, sig string) bool {
	expected := m.mac(payload)
	return hmac.Equal([]byte(expected), []byte(sig))
}
