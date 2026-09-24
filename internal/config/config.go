// Package config loads server configuration from environment variables.
package config

import "os"

type Config struct {
	Port               string
	DatabaseURL        string
	GoogleClientID     string
	GoogleClientSecret string
	GoogleRedirectURL  string
}

func Load() Config {
	return Config{
		Port:               getEnv("PORT", "8080"),
		DatabaseURL:        getEnv("DATABASE_URL", ""),
		GoogleClientID:     getEnv("GOOGLE_CLIENT_ID", ""),
		GoogleClientSecret: getEnv("GOOGLE_CLIENT_SECRET", ""),
		GoogleRedirectURL:  getEnv("GOOGLE_REDIRECT_URL", ""),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
