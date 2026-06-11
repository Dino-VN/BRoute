package config

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

var Version = "0.0.3"

type Config struct {
	Port             string
	DataDir          string
	JWTSecret        string
	StorageKey       string
	StorageKeyVer    string
	AuthCookieSecure bool
	GoVersion        string
	AppVersion       string
	WebDevProxy      string
}

func Load() Config {
	_ = ensureEnvFile()
	dataDir := getenv("DATA_DIR", defaultDataDir())
	return Config{
		Port:             getenv("PORT", "20128"),
		DataDir:          dataDir,
		JWTSecret:        strings.TrimSpace(os.Getenv("JWT_SECRET")),
		StorageKey:       strings.TrimSpace(os.Getenv("STORAGE_ENCRYPTION_KEY")),
		StorageKeyVer:    getenv("STORAGE_ENCRYPTION_KEY_VERSION", "1"),
		AuthCookieSecure: os.Getenv("AUTH_COOKIE_SECURE") == "true",
		GoVersion:        runtime.Version(),
		AppVersion:       strings.TrimPrefix(Version, "v"),
		WebDevProxy:      strings.TrimSpace(os.Getenv("WEB_DEV_PROXY")),
	}
}

func ensureEnvFile() error {
	path := envFilePath()
	if _, err := os.Stat(path); err == nil {
		return loadEnvFile(path)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	jwt, err := randomSecret(48)
	if err != nil {
		return err
	}
	storageKey, err := randomSecret(32)
	if err != nil {
		return err
	}
	content := fmt.Sprintf("JWT_SECRET=%s\nSTORAGE_ENCRYPTION_KEY=%s\nSTORAGE_ENCRYPTION_KEY_VERSION=1\n", jwt, storageKey)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return err
	}
	return loadEnvFile(path)
}

func envFilePath() string {
	if home := strings.TrimSpace(os.Getenv("BROUTE_HOME")); home != "" {
		return filepath.Join(home, ".env")
	}
	return ".env"
}

func loadEnvFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), "\"'")
		if key != "" && os.Getenv(key) == "" {
			_ = os.Setenv(key, value)
		}
	}
	return nil
}

func randomSecret(size int) (string, error) {
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
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
		return filepath.Join(".broute", "data")
	}
	return filepath.Join(home, ".broute", "data")
}
