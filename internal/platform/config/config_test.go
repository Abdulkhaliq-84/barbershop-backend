package config_test

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/config"
)

const (
	dbURL       = "DATABASE_URL=postgres://u:p@localhost:5432/db?sslmode=disable"
	otpSecret   = "OTP_SECRET=test-only-secret-0123456789abcdef-xyz"
	tokenSecret = "TOKEN_SIGNING_SECRET=test-only-token-secret-0123456789abcdef"
	mediaSecret = "MEDIA_SIGNING_SECRET=test-only-media-secret-0123456789abcdef"
)

func TestLoadDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load([]string{dbURL, otpSecret, tokenSecret, mediaSecret, "APP_ENV=development"})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Env != config.EnvDevelopment {
		t.Errorf("Env = %q, want %q", cfg.Env, config.EnvDevelopment)
	}
	if cfg.HTTP.Addr != "127.0.0.1:8080" {
		t.Errorf("HTTP.Addr = %q, want 127.0.0.1:8080", cfg.HTTP.Addr)
	}
	if cfg.HTTP.ReadHeaderTimeout != 5*time.Second {
		t.Errorf("HTTP.ReadHeaderTimeout = %v, want 5s", cfg.HTTP.ReadHeaderTimeout)
	}
	if cfg.Database.MaxConns != 10 {
		t.Errorf("Database.MaxConns = %d, want 10", cfg.Database.MaxConns)
	}
	if d := cfg.Database; d.StatementTimeout != 10*time.Second || d.LockTimeout != 5*time.Second || d.IdleInTxTimeout != 30*time.Second {
		t.Errorf("Database timeouts = %v, %v, %v; want 10s, 5s, 30s", d.StatementTimeout, d.LockTimeout, d.IdleInTxTimeout)
	}
	if cfg.Log.Level != slog.LevelInfo || cfg.Log.Format != "json" {
		t.Errorf("Log = %+v, want info/json", cfg.Log)
	}
	if cfg.Media.Dir != "var/media" {
		t.Errorf("Media.Dir = %q, want var/media", cfg.Media.Dir)
	}
}

func TestLoadOverrides(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load([]string{
		dbURL,
		otpSecret, tokenSecret, mediaSecret,
		"APP_ENV=staging",
		"HTTP_ADDR=:9000",
		"HTTP_SHUTDOWN_TIMEOUT=45s",
		"DATABASE_MAX_CONNS=25",
		"LOG_LEVEL=debug",
		"LOG_FORMAT=text",
		"MEDIA_DIR=/srv/media",
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Env != config.EnvStaging || cfg.HTTP.Addr != ":9000" ||
		cfg.HTTP.ShutdownTimeout != 45*time.Second || cfg.Database.MaxConns != 25 ||
		cfg.Log.Level != slog.LevelDebug || cfg.Log.Format != "text" || cfg.Media.Dir != "/srv/media" {
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
		{"missing database url", []string{otpSecret, tokenSecret, mediaSecret}, "DATABASE_URL"},
		{"empty database url", []string{"DATABASE_URL=", otpSecret, tokenSecret, mediaSecret}, "DATABASE_URL"},
		{"missing otp secret", []string{dbURL, tokenSecret, mediaSecret}, "OTP_SECRET"},
		{"short otp secret", []string{dbURL, "OTP_SECRET=too-short", tokenSecret, mediaSecret}, "OTP_SECRET"},
		{"missing token secret", []string{dbURL, otpSecret, mediaSecret}, "TOKEN_SIGNING_SECRET"},
		{"short token secret", []string{dbURL, otpSecret, mediaSecret, "TOKEN_SIGNING_SECRET=too-short"}, "TOKEN_SIGNING_SECRET"},
		{"dev token secret in staging", []string{dbURL, otpSecret, mediaSecret, "APP_ENV=staging", "MEDIA_DIR=/srv/media", "TOKEN_SIGNING_SECRET=local-development-token-signing-secret-not-for-real-use"}, "public development value"},
		{"dev otp secret in staging", []string{dbURL, tokenSecret, mediaSecret, "APP_ENV=staging", "MEDIA_DIR=/srv/media", "OTP_SECRET=local-development-otp-secret-not-for-real-use"}, "OTP_SECRET is the public development value"},
		{"missing media secret", []string{dbURL, otpSecret, tokenSecret}, "MEDIA_SIGNING_SECRET"},
		{"short media secret", []string{dbURL, otpSecret, tokenSecret, "MEDIA_SIGNING_SECRET=too-short"}, "MEDIA_SIGNING_SECRET"},
		{"dev media secret in production", []string{dbURL, otpSecret, tokenSecret, "APP_ENV=production", "MEDIA_DIR=/srv/media", "MEDIA_SIGNING_SECRET=local-development-media-signing-secret-not-for-real-use"}, "MEDIA_SIGNING_SECRET is the public development value"},
		{"relative media dir in staging", []string{dbURL, otpSecret, tokenSecret, mediaSecret, "APP_ENV=staging", "MEDIA_DIR=var/media"}, "MEDIA_DIR must be an absolute path"},
		{"unknown sms provider", []string{dbURL, otpSecret, tokenSecret, mediaSecret, "SMS_PROVIDER=pigeon"}, "SMS_PROVIDER"},
		{"console sms in production", []string{dbURL, otpSecret, tokenSecret, mediaSecret, "APP_ENV=production"}, "not allowed in production"},
		{"unknown environment", []string{dbURL, otpSecret, tokenSecret, mediaSecret, "APP_ENV=qa"}, "APP_ENV"},
		{"unknown log format", []string{dbURL, otpSecret, tokenSecret, mediaSecret, "LOG_FORMAT=xml"}, "LOG_FORMAT"},
		{"bad log level", []string{dbURL, otpSecret, tokenSecret, mediaSecret, "LOG_LEVEL=loud"}, "LOG_LEVEL"},
		{"bad duration", []string{dbURL, otpSecret, tokenSecret, mediaSecret, "HTTP_READ_TIMEOUT=soon"}, "HTTP_READ_TIMEOUT"},
		{"zero pool size", []string{dbURL, otpSecret, tokenSecret, mediaSecret, "DATABASE_MAX_CONNS=0"}, "DATABASE_MAX_CONNS"},
		{"a pool of one", []string{dbURL, otpSecret, tokenSecret, mediaSecret, "DATABASE_MAX_CONNS=1"}, "DATABASE_MAX_CONNS must be at least 2"},
		{"no statement timeout", []string{dbURL, otpSecret, tokenSecret, mediaSecret, "DATABASE_STATEMENT_TIMEOUT=0s"}, "DATABASE_STATEMENT_TIMEOUT must be positive"},
		{"no lock timeout", []string{dbURL, otpSecret, tokenSecret, mediaSecret, "DATABASE_LOCK_TIMEOUT=0s"}, "DATABASE_LOCK_TIMEOUT must be positive"},
		{"no idle transaction timeout", []string{dbURL, otpSecret, tokenSecret, mediaSecret, "DATABASE_IDLE_IN_TRANSACTION_SESSION_TIMEOUT=-1s"}, "DATABASE_IDLE_IN_TRANSACTION_SESSION_TIMEOUT must be positive"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := config.Load(append([]string{"APP_ENV=development"}, tt.environ...))
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

	_, err := config.Load([]string{dbURL, otpSecret, tokenSecret, mediaSecret, "APP_ENV=qa", "LOG_FORMAT=xml"})
	if err == nil {
		t.Fatal("Load() error = nil, want error")
	}
	for _, want := range []string{"APP_ENV", "LOG_FORMAT"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}
}

func TestRequiredEnvironment(t *testing.T) {
	t.Parallel()
	for _, extra := range [][]string{nil, {"APP_ENV="}} {
		_, err := config.Load(append([]string{dbURL, otpSecret, tokenSecret, mediaSecret}, extra...))
		if err == nil || !strings.Contains(err.Error(), "APP_ENV") {
			t.Fatalf("expected APP_ENV error, got %v", err)
		}
	}
}

func TestAllTimeoutsMustBePositive(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"HTTP_READ_HEADER_TIMEOUT", "HTTP_READ_TIMEOUT", "HTTP_WRITE_TIMEOUT", "HTTP_IDLE_TIMEOUT", "HTTP_SHUTDOWN_TIMEOUT", "WORKER_SHUTDOWN_TIMEOUT"} {
		for _, value := range []string{"0", "0s", "-1s", "invalid-sensitive-value", "999999999999999999999h"} {
			t.Run(key+"/"+value, func(t *testing.T) {
				t.Parallel()
				_, err := config.Load([]string{dbURL, otpSecret, tokenSecret, mediaSecret, "APP_ENV=test", key + "=" + value})
				if err == nil || !strings.Contains(err.Error(), key) {
					t.Fatalf("expected %s error, got %v", key, err)
				}
				if strings.Contains(err.Error(), value) {
					t.Fatalf("raw input leaked: %v", err)
				}
			})
		}
	}
}

func TestConfigurationErrorsDoNotEchoInput(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"APP_ENV", "LOG_FORMAT", "LOG_LEVEL", "SMS_PROVIDER", "DATABASE_MAX_CONNS"} {
		_, err := config.Load([]string{dbURL, otpSecret, tokenSecret, mediaSecret, "APP_ENV=test", key + "=+966551234567-private"})
		if err == nil || strings.Contains(err.Error(), "+966551234567-private") {
			t.Fatalf("unsafe error for %s: %v", key, err)
		}
	}
}

// The same mistakes give the same message, in the same order, every time.
func TestLoadErrorsAreStable(t *testing.T) {
	t.Parallel()
	environ := []string{
		dbURL, "HTTP_READ_TIMEOUT=0s", "HTTP_WRITE_TIMEOUT=0s", "HTTP_IDLE_TIMEOUT=0s", "DATABASE_LOCK_TIMEOUT=0s",
		"APP_ENV=production", "MEDIA_DIR=/srv/media",
		"OTP_SECRET=local-development-otp-secret-not-for-real-use",
		"TOKEN_SIGNING_SECRET=local-development-token-signing-secret-not-for-real-use",
		"MEDIA_SIGNING_SECRET=local-development-media-signing-secret-not-for-real-use",
	}
	_, first := config.Load(environ)
	if first == nil {
		t.Fatal("Load() error = nil, want error")
	}
	for range 20 {
		if _, err := config.Load(environ); err.Error() != first.Error() {
			t.Fatalf("errors changed order:\n%v\nthen\n%v", first, err)
		}
	}
}
