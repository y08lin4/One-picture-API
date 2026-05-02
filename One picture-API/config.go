package main

import (
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	Addr         string
	ImageBaseDir string
	PublicDir    string
	TokensFile   string
	StatsFile    string
	TagsFile     string
	CookieSecure bool
}

func LoadConfig() Config {
	return Config{
		Addr:         envOrDefault("OPAPI_ADDR", ":8080"),
		ImageBaseDir: envOrDefault("OPAPI_IMAGES_DIR", "images"),
		PublicDir:    envOrDefault("OPAPI_PUBLIC_DIR", "public"),
		TokensFile:   envOrDefault("OPAPI_TOKENS_FILE", "tokens.json"),
		StatsFile:    envOrDefault("OPAPI_STATS_FILE", "stats.json"),
		TagsFile:     envOrDefault("OPAPI_TAGS_FILE", tagIndexFilename),
		CookieSecure: envBool("OPAPI_COOKIE_SECURE", false),
	}
}

func envOrDefault(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func envBool(key string, fallback bool) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if value == "" {
		return fallback
	}
	switch value {
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	default:
		return fallback
	}
}

func categoryFolder(imageBaseDir, category string) string {
	return filepath.Join(imageBaseDir, category)
}

func displayListenAddr(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "http://localhost" + addr
	}
	if strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://") {
		return addr
	}
	return "http://" + addr
}

func writeFileAtomic(filename string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(filename)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(filename)+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	renamed := false
	defer func() {
		if !renamed {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	if err := os.Rename(tmpName, filename); err != nil {
		// Windows cannot replace an existing file with Rename.
		_ = os.Remove(filename)
		if err2 := os.Rename(tmpName, filename); err2 != nil {
			return err2
		}
	}
	renamed = true
	return nil
}
