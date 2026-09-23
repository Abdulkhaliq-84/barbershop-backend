package config_test

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/config"
)

const (
	dbURL     = "DATABASE_URL=postgres://u:p@localhost:5432/db?sslmode=disable"
	otpSecret = "OTP_SECRET=test-only-secret-0123456789abcdef-xyz"
)

func TestLoadDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load([]string{dbURL, otpSecret})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Env != config.EnvDevelopment {
		t.Errorf("Env = %q, want %q", cfg.Env, config.EnvDevelopment)
	}
	if cfg.HTTP.Addr != ":8080" {
		t.Errorf("HTTP.Addr = %q, want :8080", cfg.HTTP.Addr)
	}
	if cfg.HTTP.ReadHeaderTimeout != 5*time.Second {
		t.Errorf("HTTP.ReadHeaderTimeout = %v, want 5s", cfg.HTTP.ReadHeaderTimeout)
	}
	if cfg.Database.MaxConns != 10 {
		t.Errorf("Database.MaxConns = %d, want 10", cfg.Database.MaxConns)
	}
	if cfg.Log.Level != slog.LevelInfo || cfg.Log.Format != "json" {
		t.Errorf("Log = %+v, want info/json", cfg.Log)
	}
}

func TestLoadOverrides(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load([]string{
		dbURL,
		otpSecret,
		"APP_ENV=staging",
		"HTTP_ADDR=:9000",
		"HTTP_SHUTDOWN_TIMEOUT=45s",
		"DATABASE_MAX_CONNS=25",
		"LOG_LEVEL=debug",
		"LOG_FORMAT=text",
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Env != config.EnvStaging || cfg.HTTP.Addr != ":9000" ||
		cfg.HTTP.ShutdownTimeout != 45*time.Second || cfg.Database.MaxConns != 25 ||
		cfg.Log.Level != slog.LevelDebug || cfg.Log.Format != "text" {
		t.Errorf("overrides not applied: %+v", cfg)
	}
}

// A table-driven test: one table of cases, one loop, t.Run per case.
// This is the Go equivalent of jest's `it.each`.
func TestLoadErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		environ []string
		wantErr string // substring the error must contain
	}{
		{"missing database url", []string{otpSecret}, "DATABASE_URL"},
		{"empty database url", []string{"DATABASE_URL=", otpSecret}, "DATABASE_URL"},
		{"missing otp secret", []string{dbURL}, "OTP_SECRET"},
		{"short otp secret", []string{dbURL, "OTP_SECRET=too-short"}, "OTP_SECRET"},
		{"unknown sms provider", []string{dbURL, otpSecret, "SMS_PROVIDER=pigeon"}, "SMS_PROVIDER"},
		{"console sms in production", []string{dbURL, otpSecret, "APP_ENV=production"}, "not allowed in production"},
		{"unknown environment", []string{dbURL, otpSecret, "APP_ENV=qa"}, "APP_ENV"},
		{"unknown log format", []string{dbURL, otpSecret, "LOG_FORMAT=xml"}, "LOG_FORMAT"},
		{"bad log level", []string{dbURL, otpSecret, "LOG_LEVEL=loud"}, "LOG_LEVEL"},
		{"bad duration", []string{dbURL, otpSecret, "HTTP_READ_TIMEOUT=soon"}, "HTTP_READ_TIMEOUT"},
		{"zero pool size", []string{dbURL, otpSecret, "DATABASE_MAX_CONNS=0"}, "DATABASE_MAX_CONNS"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := config.Load(tt.environ)
			if err == nil {
				t.Fatalf("Load() error = nil, want error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Load() error = %q, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestLoadReportsAllProblemsAtOnce(t *testing.T) {
	t.Parallel()

	_, err := config.Load([]string{dbURL, otpSecret, "APP_ENV=qa", "LOG_FORMAT=xml"})
	if err == nil {
		t.Fatal("Load() error = nil, want error")
	}
	for _, want := range []string{"APP_ENV", "LOG_FORMAT"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}
}
