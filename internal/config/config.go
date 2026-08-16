package config

import (
	"bufio"
	"net/url"
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
	KnowledgeDir  string
}

// Load reads configuration from environment variables.
func Load() Config {
	origins := splitCSV(envOr("CORS_ORIGINS", "http://localhost:5184,http://127.0.0.1:5184"))
	return Config{
		Addr:          envOr("ADDR", ":8134"),
		DatabaseURL:   os.Getenv("DATABASE_URL"),
		JWTSecret:     envOr("JWT_SECRET", "dev-jwt-secret-change-me"),
		MigrationsDir: envOr("MIGRATIONS_DIR", "migrations"),
		ChatBaseURL:   ResolveChatBaseURL(envOr("CHAT_BASE_URL", "http://127.0.0.1:8130/v1")),
		ChatAPIKey:    os.Getenv("CHAT_API_KEY"),
		ChatModel:     envOr("CHAT_MODEL", "auto"),
		CORSOrigins:   origins,
		KnowledgeDir:  envOr("KNOWLEDGE_DIR", `C:\Users\armin\TFS\Source-NewUI\docs`),
	}
}

// ResolveChatBaseURL trims a chat gateway base URL. Inside a container, loopback
// hosts are rewritten to host.docker.internal so the process reaches the host gateway.
func ResolveChatBaseURL(raw string) string {
	return resolveChatBaseURL(raw, runningInContainer())
}

func runningInContainer() bool {
	_, err := os.Stat("/.dockerenv")
	return err == nil
}

func resolveChatBaseURL(raw string, isContainer bool) string {
	base := strings.TrimRight(strings.TrimSpace(raw), "/")
	if base == "" || !isContainer {
		return base
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Host == "" {
		return base
	}
	hostName := parsed.Hostname()
	if hostName != "127.0.0.1" && hostName != "localhost" && hostName != "::1" {
		return base
	}
	if port := parsed.Port(); port != "" {
		parsed.Host = "host.docker.internal:" + port
	} else {
		parsed.Host = "host.docker.internal"
	}
	return strings.TrimRight(parsed.String(), "/")
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
