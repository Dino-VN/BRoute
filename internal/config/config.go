package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type Config struct {
	Port             string
	DataDir          string
	JWTSecret        string
	AuthCookieSecure bool
	GoVersion        string
	WebDevProxy      string
}

func Load() Config {
	dataDir := getenv("DATA_DIR", defaultDataDir())
	return Config{
		Port:             getenv("PORT", "20128"),
		DataDir:          dataDir,
		JWTSecret:        strings.TrimSpace(os.Getenv("JWT_SECRET")),
		AuthCookieSecure: os.Getenv("AUTH_COOKIE_SECURE") == "true",
		GoVersion:        runtime.Version(),
		WebDevProxy:      strings.TrimSpace(os.Getenv("WEB_DEV_PROXY")),
	}
}

func getenv(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func defaultDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".broute"
	}
	return filepath.Join(home, ".broute")
}
