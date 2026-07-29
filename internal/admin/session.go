package admin

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"
)

// SessionCookieName is the name of the HTTP cookie that carries the admin
// session token.
const SessionCookieName = "admin_session"

// ActiveArtistCookieName is the name of the HTTP cookie that carries the
// currently selected artist for the admin session.
const ActiveArtistCookieName = "admin_active_artist"

// SessionLifetime is the maximum age of a valid admin session.
const SessionLifetime = 24 * time.Hour

const (
	sessionPurposeAdmin         = "admin"
	sessionPurposePlatformAdmin = "platform_admin"
	sessionPurposeActiveArtist  = "active_artist"
)

// Session holds the claims embedded in a signed admin session token.
type Session struct {
	UserID   int64  `json:"user_id"`
	Email    string `json:"email"`
	IssuedAt int64  `json:"issued_at"`
	Purpose  string `json:"purpose"`
}

// ActiveArtist holds the claims embedded in a signed active artist token.
type ActiveArtist struct {
	UserID     int64  `json:"user_id"`
	ArtistSlug string `json:"artist_slug"`
	IssuedAt   int64  `json:"issued_at"`
	Purpose    string `json:"purpose"`
}

// NewToken creates a signed session token for the given user and returns it as
// a base64url-encoded string.
func NewToken(secret []byte, userID int64, email string, issuedAt time.Time) (string, error) {
	payload, err := json.Marshal(Session{
		UserID:   userID,
		Email:    email,
		IssuedAt: issuedAt.Unix(),
		Purpose:  sessionPurposeAdmin,
	})
	if err != nil {
		return "", err
	}

	signature := sign(secret, payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

// NewPlatformToken creates a signed platform-admin token. Platform admins are
// identified by the configured username rather than a database user ID.
func NewPlatformToken(secret []byte, username string, issuedAt time.Time) (string, error) {
	payload, err := json.Marshal(Session{
		Email:    username,
		IssuedAt: issuedAt.Unix(),
		Purpose:  sessionPurposePlatformAdmin,
	})
	if err != nil {
		return "", err
	}

	signature := sign(secret, payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

// NewActiveArtistToken creates a signed token for the user's active artist.
func NewActiveArtistToken(secret []byte, userID int64, artistSlug string, issuedAt time.Time) (string, error) {
	payload, err := json.Marshal(ActiveArtist{
		UserID:     userID,
		ArtistSlug: artistSlug,
		IssuedAt:   issuedAt.Unix(),
		Purpose:    sessionPurposeActiveArtist,
	})
	if err != nil {
		return "", err
	}

	signature := sign(secret, payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

// ParseToken verifies an artist-admin token and returns its claims.
func ParseToken(secret []byte, token string) (Session, error) {
	var session Session
	if err := parseSignedToken(secret, token, &session); err != nil {
		return session, err
	}
	if session.Purpose != sessionPurposeAdmin || session.UserID <= 0 || session.Email == "" {
		return session, errors.New("invalid admin token payload")
	}
	return session, nil
}

// ParsePlatformToken verifies a platform-admin token and returns its claims.
func ParsePlatformToken(secret []byte, token string) (Session, error) {
	var session Session
	if err := parseSignedToken(secret, token, &session); err != nil {
		return session, err
	}
	if session.Purpose != sessionPurposePlatformAdmin || session.UserID != 0 || session.Email == "" {
		return session, errors.New("invalid platform admin token payload")
	}
	return session, nil
}

// ParseActiveArtistToken verifies token and returns the embedded active artist.
func ParseActiveArtistToken(secret []byte, token string) (ActiveArtist, error) {
	var activeArtist ActiveArtist

	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return activeArtist, errors.New("invalid token format")
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return activeArtist, err
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return activeArtist, err
	}

	expected := sign(secret, payload)
	if !hmac.Equal(signature, expected) {
		return activeArtist, errors.New("invalid token signature")
	}
	if err := json.Unmarshal(payload, &activeArtist); err != nil {
		return activeArtist, err
	}
	if activeArtist.Purpose != sessionPurposeActiveArtist || activeArtist.UserID <= 0 || activeArtist.ArtistSlug == "" {
		return activeArtist, errors.New("invalid token payload")
	}
	if activeArtist.IssuedAt == 0 {
		return activeArtist, errors.New("invalid token issue time")
	}
	age := time.Since(time.Unix(activeArtist.IssuedAt, 0))
	if age < 0 || age > SessionLifetime {
		return activeArtist, errors.New("expired token")
	}

	return activeArtist, nil
}

func parseSignedToken(secret []byte, token string, destination any) error {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return errors.New("invalid token format")
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return err
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return err
	}
	if !hmac.Equal(signature, sign(secret, payload)) {
		return errors.New("invalid token signature")
	}
	if err := json.Unmarshal(payload, destination); err != nil {
		return err
	}

	var issuedAtClaims struct {
		IssuedAt int64 `json:"issued_at"`
	}
	if err := json.Unmarshal(payload, &issuedAtClaims); err != nil {
		return err
	}
	if issuedAtClaims.IssuedAt == 0 {
		return errors.New("invalid token issue time")
	}
	age := time.Since(time.Unix(issuedAtClaims.IssuedAt, 0))
	if age < 0 || age > SessionLifetime {
		return errors.New("expired token")
	}
	return nil
}

// HashUserPassword hashes a user password with Argon2id and returns an encoded
// string containing the parameters, salt, and digest.
func HashUserPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate argon2id salt: %w", err)
	}
	const (
		argonTime    = 3
		argonMemory  = 64 * 1024
		argonThreads = 4
		argonKeyLen  = 32
	)
	hash := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	encoded := fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	)
	return encoded, nil
}

// VerifyPassword reports whether password matches the Argon2id-encoded storedHash.
func VerifyPassword(password, storedHash string) bool {
	parts := strings.SplitN(storedHash, "$", 6)
	// expected: ["", "argon2id", "v=19", "m=...,t=...,p=...", "<salt>", "<hash>"]
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false
	}
	var memory, timeCost uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &timeCost, &threads); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	hashBytes, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}
	computed := argon2.IDKey([]byte(password), salt, timeCost, memory, threads, uint32(len(hashBytes)))
	return subtle.ConstantTimeCompare(computed, hashBytes) == 1
}

// sign returns the HMAC-SHA256 of value keyed by secret.
func sign(secret, value []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write(value)
	return mac.Sum(nil)
}

// hashInvitationCode returns the HMAC-SHA256 hex digest of code keyed by secret.
func hashInvitationCode(secret []byte, code string) string {
	return hex.EncodeToString(sign(secret, []byte(code)))
}
