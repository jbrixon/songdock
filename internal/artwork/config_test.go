package artwork

import "testing"

func TestLoadConfigDefaultsToFilesystem(t *testing.T) {
	cfg, err := loadConfig(func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Driver != "filesystem" || cfg.Dir != DefaultDir {
		t.Fatalf("defaults = driver %q, dir %q", cfg.Driver, cfg.Dir)
	}
}

func TestLoadConfigUsesConfiguredFilesystemDirectory(t *testing.T) {
	cfg, err := loadConfig(func(name string) string {
		if name == "ARTWORK_DIR" {
			return "/tmp/custom-artwork"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Driver != "filesystem" || cfg.Dir != "/tmp/custom-artwork" {
		t.Fatalf("config = driver %q, dir %q", cfg.Driver, cfg.Dir)
	}
}

func TestLoadConfigDoesNotInferS3FromCredentials(t *testing.T) {
	cfg, err := loadConfig(func(name string) string {
		values := map[string]string{
			"S3_BUCKET":            "bucket",
			"S3_ACCESS_KEY_ID":     "access",
			"S3_SECRET_ACCESS_KEY": "secret",
		}
		return values[name]
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Driver != "filesystem" {
		t.Fatalf("driver = %q, want filesystem", cfg.Driver)
	}
}

func TestLoadConfigValidatesS3Driver(t *testing.T) {
	base := map[string]string{
		"ARTWORK_STORAGE_DRIVER": "s3",
		"S3_BUCKET":              "bucket",
		"S3_ACCESS_KEY_ID":       "access",
		"S3_SECRET_ACCESS_KEY":   "secret",
	}
	cfg, err := loadConfig(func(name string) string { return base[name] })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Driver != "s3" || cfg.Region != "us-east-1" {
		t.Fatalf("S3 config = %+v", cfg)
	}

	for _, missing := range []string{"S3_BUCKET", "S3_ACCESS_KEY_ID", "S3_SECRET_ACCESS_KEY"} {
		values := map[string]string{}
		for key, value := range base {
			values[key] = value
		}
		delete(values, missing)
		_, err := loadConfig(func(name string) string { return values[name] })
		if err == nil {
			t.Fatalf("missing %s was accepted", missing)
		}
	}
}

func TestLoadConfigRejectsUnknownDriverAndBadValues(t *testing.T) {
	_, err := loadConfig(func(name string) string {
		if name == "ARTWORK_STORAGE_DRIVER" {
			return "azure"
		}
		return ""
	})
	if err == nil {
		t.Fatal("unknown driver was accepted")
	}

	_, err = loadConfig(func(name string) string {
		if name == "S3_FORCE_PATH_STYLE" {
			return "sometimes"
		}
		return ""
	})
	if err == nil {
		t.Fatal("bad path-style value was accepted")
	}

	_, err = loadConfig(func(name string) string {
		values := map[string]string{
			"ARTWORK_STORAGE_DRIVER": "s3",
			"S3_BUCKET":              "bucket",
			"S3_ACCESS_KEY_ID":       "access",
			"S3_SECRET_ACCESS_KEY":   "secret",
			"S3_PREFIX":              "artwork/../outside",
		}
		return values[name]
	})
	if err == nil {
		t.Fatal("unsafe S3 prefix was accepted")
	}
}
