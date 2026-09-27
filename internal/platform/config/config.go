// Package config loads the service configuration from environment variables
// (12-factor) into a typed struct and validates it once, at startup.
//
// Node.js analogy: dotenv + zod in one step — but there is no .env loading
// here on purpose; the environment is the only source of configuration.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
)

// Environments the service knows about.
const (
	EnvDevelopment = "development"
	EnvTest        = "test"
	EnvStaging     = "staging"
	EnvProduction  = "production"
)

// Config is the complete service configuration.
type Config struct {
	Env      string `env:"APP_ENV,required,notEmpty"`
	HTTP     HTTP
	Database Database
	Log      Log
	Auth     Auth
}

// HTTP configures the HTTP server. The timeouts protect the server from slow
// or stuck clients; Go's http.Server has no timeouts by default.
type HTTP struct {
	Addr              string        `env:"HTTP_ADDR"                envDefault:"127.0.0.1:8080"`
	ReadHeaderTimeout time.Duration `env:"HTTP_READ_HEADER_TIMEOUT" envDefault:"5s"`
	ReadTimeout       time.Duration `env:"HTTP_READ_TIMEOUT"        envDefault:"15s"`
	WriteTimeout      time.Duration `env:"HTTP_WRITE_TIMEOUT"       envDefault:"15s"`
	IdleTimeout       time.Duration `env:"HTTP_IDLE_TIMEOUT"        envDefault:"60s"`
	ShutdownTimeout   time.Duration `env:"HTTP_SHUTDOWN_TIMEOUT"    envDefault:"20s"`
}

// Database configures the PostgreSQL connection pool.
type Database struct {
	// URL is a libpq connection string, e.g.
	// postgres://user:pass@localhost:5432/barbershop?sslmode=disable.
	// It contains a password: never log it.
	URL      string `env:"DATABASE_URL,required,notEmpty"`
	MaxConns int32  `env:"DATABASE_MAX_CONNS" envDefault:"10"`
}

// Auth configures login.
type Auth struct {
	// OTPSecret is the HMAC key for stored one-time codes (≥ 32 bytes).
	// A secret: never log it, and use a different one per environment.
	OTPSecret string `env:"OTP_SECRET,required,notEmpty"`
	// SMSProvider delivers codes. Only "console" (development: prints the
	// code to the log) exists until a real provider arrives in M7.
	SMSProvider string `env:"SMS_PROVIDER" envDefault:"console"`
	// TokenSecret derives the Ed25519 key that signs access tokens (≥ 32
	// bytes). Whoever knows it can sign in as anyone: treat it like a password.
	TokenSecret string `env:"TOKEN_SIGNING_SECRET,required,notEmpty"`
}

// devSecrets are the local-development values published in the Makefile,
// compose.yaml and .env.example. Anyone can read them, so they are refused
// outside development and test.
var devSecrets = map[string]string{
	"OTP_SECRET":           "local-development-otp-secret-not-for-real-use",
	"TOKEN_SIGNING_SECRET": "local-development-token-signing-secret-not-for-real-use",
}

// Log configures structured logging.
type Log struct {
	// Level accepts debug, info, warn or error (slog.Level parses itself).
	Level  slog.Level `env:"LOG_LEVEL"  envDefault:"info"`
	Format string     `env:"LOG_FORMAT" envDefault:"json"` // json | text
}

// Load reads the configuration from environ, a list of KEY=value pairs in the
// same shape as os.Environ(). Taking the environment as a parameter (instead
// of reading os.Getenv inside) keeps Load pure and easy to test.
func Load(environ []string) (Config, error) {
	var cfg Config
	opts := env.Options{Environment: env.ToMap(environ)}
	if err := env.ParseWithOptions(&cfg, opts); err != nil {
		return Config{}, fmt.Errorf("parse environment: %w", withEnvNames(err))
	}
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// validate checks the rules the struct tags cannot express. It reports every
// problem at once (errors.Join) so a misconfigured deploy is fixed in one go.
func (c Config) validate() error {
	var errs []error
	if !slices.Contains([]string{EnvDevelopment, EnvTest, EnvStaging, EnvProduction}, c.Env) {
		errs = append(errs, errors.New("APP_ENV must be development, test, staging or production"))
	}
	if !slices.Contains([]string{"json", "text"}, c.Log.Format) {
		errs = append(errs, errors.New("LOG_FORMAT must be json or text"))
	}
	if c.Database.MaxConns < 1 {
		errs = append(errs, errors.New("DATABASE_MAX_CONNS must be at least 1"))
	}
	for key, value := range map[string]time.Duration{
		"HTTP_READ_HEADER_TIMEOUT": c.HTTP.ReadHeaderTimeout,
		"HTTP_READ_TIMEOUT":        c.HTTP.ReadTimeout,
		"HTTP_WRITE_TIMEOUT":       c.HTTP.WriteTimeout,
		"HTTP_IDLE_TIMEOUT":        c.HTTP.IdleTimeout,
		"HTTP_SHUTDOWN_TIMEOUT":    c.HTTP.ShutdownTimeout,
	} {
		if value <= 0 {
			errs = append(errs, fmt.Errorf("%s must be positive", key))
		}
	}
	if len(c.Auth.OTPSecret) < 32 {
		errs = append(errs, errors.New("OTP_SECRET must be at least 32 bytes"))
	}
	if len(c.Auth.TokenSecret) < 32 {
		errs = append(errs, errors.New("TOKEN_SIGNING_SECRET must be at least 32 bytes"))
	}
	if c.Env == EnvStaging || c.Env == EnvProduction {
		for key, value := range map[string]string{"OTP_SECRET": c.Auth.OTPSecret, "TOKEN_SIGNING_SECRET": c.Auth.TokenSecret} {
			if value == devSecrets[key] {
				errs = append(errs, fmt.Errorf("%s is the public development value; set a real secret", key))
			}
		}
	}
	if c.Auth.SMSProvider != "console" {
		errs = append(errs, errors.New("SMS_PROVIDER must be console (the only provider so far)"))
	}
	if c.Env == EnvProduction && c.Auth.SMSProvider == "console" {
		errs = append(errs, errors.New("SMS_PROVIDER=console prints login codes to the log and is not allowed in production"))
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}
	return nil
}

// withEnvNames rewrites the library's parse errors, which name Go struct
// fields ("ReadTimeout"), to name the environment variable the operator must
// fix ("HTTP_READ_TIMEOUT").
//
// errors.As is Go's typed error check — like `err instanceof ParseError` in
// JavaScript, but it also looks through wrapped errors.
func withEnvNames(err error) error {
	var agg env.AggregateError
	if !errors.As(err, &agg) {
		return err
	}
	keys := envKeys(reflect.TypeFor[Config]())
	errs := make([]error, 0, len(agg.Errors))
	for _, e := range agg.Errors {
		var pe env.ParseError
		if errors.As(e, &pe) && keys[pe.Name] != "" {
			e = fmt.Errorf("%s: invalid value", keys[pe.Name])
		}
		errs = append(errs, e)
	}
	return errors.Join(errs...)
}

// envKeys maps each struct field name to its `env` tag, walking nested
// structs. Field names are unique across Config (TestEnvKeysAreUnique).
func envKeys(t reflect.Type) map[string]string {
	keys := make(map[string]string)
	for f := range t.Fields() {
		if f.Type.Kind() == reflect.Struct && f.Tag.Get("env") == "" {
			for name, key := range envKeys(f.Type) {
				keys[name] = key
			}
			continue
		}
		if tag := f.Tag.Get("env"); tag != "" {
			key, _, _ := strings.Cut(tag, ",")
			keys[f.Name] = key
		}
	}
	return keys
}
