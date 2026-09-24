package config

import "testing"

func TestLoadDefaults(t *testing.T) {
	t.Setenv("PORT", "")
	t.Setenv("DATABASE_URL", "")

	cfg := Load()

	if cfg.Port != "8080" {
		t.Errorf("Port = %q, want default %q", cfg.Port, "8080")
	}
	if cfg.DatabaseURL != "" {
		t.Errorf("DatabaseURL = %q, want empty when unset", cfg.DatabaseURL)
	}
}

func TestLoadFromEnv(t *testing.T) {
	t.Setenv("PORT", "9090")
	t.Setenv("DATABASE_URL", "postgres://example")

	cfg := Load()

	if cfg.Port != "9090" {
		t.Errorf("Port = %q, want %q", cfg.Port, "9090")
	}
	if cfg.DatabaseURL != "postgres://example" {
		t.Errorf("DatabaseURL = %q, want %q", cfg.DatabaseURL, "postgres://example")
	}
}

func TestLoadGoogleOAuthFromEnv(t *testing.T) {
	t.Setenv("GOOGLE_CLIENT_ID", "client-id")
	t.Setenv("GOOGLE_CLIENT_SECRET", "client-secret")
	t.Setenv("GOOGLE_REDIRECT_URL", "http://localhost:8080/auth/google/callback")

	cfg := Load()

	if cfg.GoogleClientID != "client-id" {
		t.Errorf("GoogleClientID = %q, want %q", cfg.GoogleClientID, "client-id")
	}
	if cfg.GoogleClientSecret != "client-secret" {
		t.Errorf("GoogleClientSecret = %q, want %q", cfg.GoogleClientSecret, "client-secret")
	}
	if cfg.GoogleRedirectURL != "http://localhost:8080/auth/google/callback" {
		t.Errorf("GoogleRedirectURL = %q, want %q", cfg.GoogleRedirectURL, "http://localhost:8080/auth/google/callback")
	}
}

func TestLoadGoogleOAuthDefaultsEmpty(t *testing.T) {
	t.Setenv("GOOGLE_CLIENT_ID", "")
	t.Setenv("GOOGLE_CLIENT_SECRET", "")
	t.Setenv("GOOGLE_REDIRECT_URL", "")

	cfg := Load()

	if cfg.GoogleClientID != "" || cfg.GoogleClientSecret != "" || cfg.GoogleRedirectURL != "" {
		t.Errorf("Google fields = %q/%q/%q, want all empty when unset", cfg.GoogleClientID, cfg.GoogleClientSecret, cfg.GoogleRedirectURL)
	}
}
