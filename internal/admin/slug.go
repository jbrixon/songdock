package admin

import (
	"errors"
	"strconv"
	"strings"
	"unicode"
)

const maxSlugAttempts = 1000

// slugify converts a human-readable title into a URL-safe slug composed of
// lowercase letters, digits, and hyphens.
func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var out []rune
	lastDash := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			out = append(out, r)
			lastDash = false
			continue
		}
		if !lastDash {
			out = append(out, '-')
			lastDash = true
		}
	}
	return strings.Trim(string(out), "-")
}

// uniqueSongSlug derives a URL slug from title and ensures it is unique within
// the given artist's song list by appending an incrementing numeric suffix when
// necessary.
func uniqueSongSlug(repo Repository, artistID int64, title string) (string, error) {
	base := slugify(title)
	if base == "" {
		return "", errors.New("unable to generate slug from title")
	}

	for i := 0; i < maxSlugAttempts; i++ {
		candidate := base
		if i > 0 {
			candidate = base + "-" + strconv.Itoa(i)
		}

		exists, err := repo.SongSlugExists(artistID, candidate)
		if err != nil {
			return "", err
		}
		if !exists {
			return candidate, nil
		}
	}

	return "", errors.New("could not generate a unique slug")
}

func slugCandidate(base string, attempt int) string {
	if attempt == 0 {
		return base
	}
	return base + "-" + strconv.Itoa(attempt+1)
}
