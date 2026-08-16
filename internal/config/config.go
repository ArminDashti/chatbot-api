package config

import (
	"bufio"
	"os"
	"strings"
)

// LoadDotEnv loads KEY=VALUE pairs from a .env file into the process environment
// when the key is not already set. Missing file is ignored.
func LoadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		_ = os.Setenv(key, val)
	}
}

// Config holds runtime settings for the API.
type Config struct {
	Addr          string
	DatabaseURL   string
	JWTSecret     string
	MigrationsDir string
	ChatBaseURL   string
	ChatAPIKey    string
	ChatModel     string
	CORSOrigins   []string
}

// Load reads configuration from environment variables.
func Load() Config {
	origins := splitCSV(envOr("CORS_ORIGINS", "http://localhost:5184,http://127.0.0.1:5184"))
	return Config{
		Addr:          envOr("ADDR", ":8134"),
		DatabaseURL:   os.Getenv("DATABASE_URL"),
		JWTSecret:     envOr("JWT_SECRET", "dev-jwt-secret-change-me"),
		MigrationsDir: envOr("MIGRATIONS_DIR", "migrations"),
		ChatBaseURL:   strings.TrimRight(envOr("CHAT_BASE_URL", "http://127.0.0.1:8130/v1"), "/"),
		ChatAPIKey:    os.Getenv("CHAT_API_KEY"),
		ChatModel:     envOr("CHAT_MODEL", "auto"),
		CORSOrigins:   origins,
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func splitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
