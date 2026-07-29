package platformadmin

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// HashInvitationCode returns the HMAC-SHA256 hex digest of code keyed by secret.
func HashInvitationCode(secret []byte, code string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(code))
	return hex.EncodeToString(mac.Sum(nil))
}
