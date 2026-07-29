package urlpolicy

import (
	"errors"
	"net/url"
	"strings"
)

// NormalizeExternalURL returns a canonical external URL or an error if the
// value is non-empty but not a safe absolute HTTP(S) URL.
func NormalizeExternalURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}

	u, err := url.Parse(raw)
	if err != nil || u == nil {
		return "", errors.New("must be a valid absolute URL")
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return "", errors.New("must use http or https")
	}
	if u.Host == "" {
		return "", errors.New("must include a host")
	}
	return u.String(), nil
}

// SafeExternalURL returns the normalized URL or an empty string if invalid.
func SafeExternalURL(raw string) string {
	normalized, err := NormalizeExternalURL(raw)
	if err != nil {
		return ""
	}
	return normalized
}
