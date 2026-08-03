package artwork

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
)

const DefaultDir = "/data/uploads/artwork"

type Config struct {
	Driver          string
	Dir             string
	Endpoint        string
	Region          string
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string
	Prefix          string
	PublicURL       string
	ForcePathStyle  bool
}

func LoadConfig() (Config, error) {
	return loadConfig(os.Getenv)
}

func loadConfig(getenv func(string) string) (Config, error) {
	cfg := Config{
		Driver:          strings.TrimSpace(getenv("ARTWORK_STORAGE_DRIVER")),
		Dir:             strings.TrimSpace(getenv("ARTWORK_DIR")),
		Endpoint:        strings.TrimSpace(getenv("S3_ENDPOINT")),
		Region:          strings.TrimSpace(getenv("S3_REGION")),
		Bucket:          strings.TrimSpace(getenv("S3_BUCKET")),
		AccessKeyID:     strings.TrimSpace(getenv("S3_ACCESS_KEY_ID")),
		SecretAccessKey: strings.TrimSpace(getenv("S3_SECRET_ACCESS_KEY")),
		Prefix:          strings.Trim(strings.TrimSpace(getenv("S3_PREFIX")), "/"),
		PublicURL:       strings.TrimRight(strings.TrimSpace(getenv("S3_PUBLIC_URL")), "/"),
	}
	if cfg.Driver == "" {
		cfg.Driver = "filesystem"
	}
	if cfg.Driver != "filesystem" && cfg.Driver != "s3" {
		return Config{}, fmt.Errorf("ARTWORK_STORAGE_DRIVER must be filesystem or s3, got %q", cfg.Driver)
	}
	if cfg.Dir == "" {
		cfg.Dir = DefaultDir
	}
	if cfg.Endpoint != "" {
		if err := validHTTPURL("S3_ENDPOINT", cfg.Endpoint); err != nil {
			return Config{}, err
		}
	}
	if cfg.PublicURL != "" {
		if err := validHTTPURL("S3_PUBLIC_URL", cfg.PublicURL); err != nil {
			return Config{}, err
		}
	}
	if strings.ContainsAny(cfg.Prefix, `\\`) || hasDotPathSegment(cfg.Prefix) {
		return Config{}, fmt.Errorf("S3_PREFIX must not contain . or .. path segments")
	}
	forcePathStyle := strings.TrimSpace(getenv("S3_FORCE_PATH_STYLE"))
	if forcePathStyle == "" {
		cfg.ForcePathStyle = false
	} else {
		parsed, err := strconv.ParseBool(forcePathStyle)
		if err != nil {
			return Config{}, fmt.Errorf("S3_FORCE_PATH_STYLE must be a boolean: %w", err)
		}
		cfg.ForcePathStyle = parsed
	}
	if cfg.Driver == "s3" {
		missing := make([]string, 0, 3)
		for name, value := range map[string]string{
			"S3_BUCKET":            cfg.Bucket,
			"S3_ACCESS_KEY_ID":     cfg.AccessKeyID,
			"S3_SECRET_ACCESS_KEY": cfg.SecretAccessKey,
		} {
			if value == "" {
				missing = append(missing, name)
			}
		}
		if len(missing) > 0 {
			return Config{}, fmt.Errorf("missing required S3 configuration: %s", strings.Join(missing, ", "))
		}
		if cfg.Region == "" {
			cfg.Region = "us-east-1"
		}
	}
	return cfg, nil
}

func validHTTPURL(name, value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("%s must be an absolute http or https URL", name)
	}
	return nil
}

func hasDotPathSegment(value string) bool {
	for _, segment := range strings.Split(value, "/") {
		if segment == "." || segment == ".." {
			return true
		}
	}
	return false
}
