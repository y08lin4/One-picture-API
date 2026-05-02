package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Addr            string
	ImageBaseDir    string
	PublicDir       string
	TokensFile      string
	StatsFile       string
	TagsFile        string
	CookieSecure    bool
	TrustedOrigins  []string
	TrustProxy      bool
	LoginMaxFails   int
	LoginWindow     time.Duration
	LoginBlock      time.Duration
	MaxStorageBytes int64
}

func LoadConfig() Config {
	return Config{
		// 默认只监听本机，公网部署建议放在 Nginx/Caddy 等反代后面；
		// 如需直接对外监听，可显式设置 OPAPI_ADDR=:8080。
		Addr:            envOrDefault("OPAPI_ADDR", "127.0.0.1:8080"),
		ImageBaseDir:    envOrDefault("OPAPI_IMAGES_DIR", "images"),
		PublicDir:       envOrDefault("OPAPI_PUBLIC_DIR", "public"),
		TokensFile:      envOrDefault("OPAPI_TOKENS_FILE", "tokens.json"),
		StatsFile:       envOrDefault("OPAPI_STATS_FILE", "stats.json"),
		TagsFile:        envOrDefault("OPAPI_TAGS_FILE", tagIndexFilename),
		CookieSecure:    envBool("OPAPI_COOKIE_SECURE", false),
		TrustedOrigins:  splitList(os.Getenv("OPAPI_TRUSTED_ORIGINS")),
		TrustProxy:      envBool("OPAPI_TRUST_PROXY", false),
		LoginMaxFails:   envInt("OPAPI_LOGIN_MAX_FAILS", 8),
		LoginWindow:     envDuration("OPAPI_LOGIN_WINDOW", 10*time.Minute),
		LoginBlock:      envDuration("OPAPI_LOGIN_BLOCK", 15*time.Minute),
		MaxStorageBytes: envInt64("OPAPI_MAX_STORAGE_BYTES", 0),
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

func envInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return n
}

func envInt64(key string, fallback int64) int64 {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return fallback
	}
	return n
}

func envDuration(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return d
}

func splitList(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		switch r {
		case ',', '，', ';', '；', '\n', '\r', '\t', ' ':
			return true
		default:
			return false
		}
	})
	res := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			res = append(res, v)
		}
	}
	return res
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
