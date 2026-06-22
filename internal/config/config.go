package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config holds all runtime configuration loaded from environment variables.
type Config struct {
	Secret           string
	AdminUser        string
	AdminPass        string
	SiteName         string
	SiteURL          string
	LogoURL          string
	SMTPHost         string
	SMTPPort         int
	SMTPUser         string
	SMTPPass         string
	SMTPFrom         string
	DataDir          string
	Port             int
	LogLevel         string
	CheckConcurrency int
	RetentionDays    int
	TZ               string
}

func Load() (*Config, error) {
	c := &Config{
		Secret:    getEnv("RELAY_SECRET", ""),
		AdminUser: getEnv("RELAY_ADMIN_USER", "admin"),
		AdminPass: getEnv("RELAY_ADMIN_PASS", ""),

		SiteName: getEnv("RELAY_SITE_NAME", "Status"),
		SiteURL:  getEnv("RELAY_SITE_URL", "http://localhost:8080"),
		LogoURL:  getEnv("RELAY_LOGO_URL", ""),

		SMTPHost: getEnv("RELAY_SMTP_HOST", ""),
		SMTPUser: getEnv("RELAY_SMTP_USER", ""),
		SMTPPass: getEnv("RELAY_SMTP_PASS", ""),
		SMTPFrom: getEnv("RELAY_SMTP_FROM", ""),

		DataDir:  getEnv("RELAY_DATA", "./data"),
		LogLevel: getEnv("RELAY_LOG_LEVEL", "info"),
		TZ:       getEnv("RELAY_TZ", "UTC"),
	}

	var err error
	if c.SMTPPort, err = getEnvInt("RELAY_SMTP_PORT", 587); err != nil {
		return nil, fmt.Errorf("RELAY_SMTP_PORT: %w", err)
	}
	if c.Port, err = getEnvInt("RELAY_PORT", 8080); err != nil {
		return nil, fmt.Errorf("RELAY_PORT: %w", err)
	}
	if c.CheckConcurrency, err = getEnvInt("RELAY_CHECK_CONCURRENCY", 20); err != nil {
		return nil, fmt.Errorf("RELAY_CHECK_CONCURRENCY: %w", err)
	}
	if c.RetentionDays, err = getEnvInt("RELAY_RETENTION_DAYS", 90); err != nil {
		return nil, fmt.Errorf("RELAY_RETENTION_DAYS: %w", err)
	}

	if c.Secret == "" {
		return nil, fmt.Errorf("RELAY_SECRET is required (generate with: openssl rand -hex 16)")
	}
	if c.AdminPass == "" {
		return nil, fmt.Errorf("RELAY_ADMIN_PASS is required")
	}

	return c, nil
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getEnvInt(key string, def int) (int, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	return strconv.Atoi(v)
}
