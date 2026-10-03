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
	"path/filepath"
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
	Media    Media
	Worker   Worker
	Push     Push
}

// Push configures notifications to devices.
type Push struct {
	// Provider delivers them: "console" writes them to the log (development
	// only), "fcm" sends them through Firebase Cloud Messaging.
	Provider string `env:"PUSH_PROVIDER" envDefault:"console"`
	// FCMCredentialsFile is the absolute path of the Firebase service
	// account's JSON key (PUSH_PROVIDER=fcm). A secret: whoever holds it can
	// push to every install of the app. Mount it; never bake it into an
	// image or commit it.
	FCMCredentialsFile string `env:"FCM_CREDENTIALS_FILE"`
	// FCMTimeout bounds each request to Google, connecting included.
	FCMTimeout time.Duration `env:"FCM_TIMEOUT" envDefault:"5s"`
}

// Worker configures the background worker role (outbox deliveries).
type Worker struct {
	// StopTimeout is how long running jobs get to finish on shutdown; jobs
	// still running after it are cancelled and retried later.
	StopTimeout time.Duration `env:"WORKER_SHUTDOWN_TIMEOUT" envDefault:"20s"`
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
	URL string `env:"DATABASE_URL,required,notEmpty"`
	// MaxConns must be at least 2 (River's migration holds one while it
	// works on another); in the worker, River keeps a few for itself and
	// runs jobs on the rest.
	MaxConns int32 `env:"DATABASE_MAX_CONNS" envDefault:"10"`
	// The api and worker roles set these on every connection, so a slow
	// query, a lock wait or a forgotten transaction can't hold a pooled
	// connection for ever. Migrations run without them.
	StatementTimeout time.Duration `env:"DATABASE_STATEMENT_TIMEOUT"                   envDefault:"10s"`
	LockTimeout      time.Duration `env:"DATABASE_LOCK_TIMEOUT"                        envDefault:"5s"`
	IdleInTxTimeout  time.Duration `env:"DATABASE_IDLE_IN_TRANSACTION_SESSION_TIMEOUT" envDefault:"30s"`
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

// Media configures file storage.
type Media struct {
	// Dir is where uploaded files are kept. Relative paths are fine in
	// development (var/media); staging and production must use an absolute
	// path on a persistent volume.
	Dir string `env:"MEDIA_DIR" envDefault:"var/media"`
	// SigningSecret signs download links (≥ 32 bytes). Whoever knows it can
	// make links to any private file, such as CR documents.
	SigningSecret string `env:"MEDIA_SIGNING_SECRET,required,notEmpty"`
}

// devSecrets are the local-development values published in the Makefile,
// compose.yaml and .env.example. Anyone can read them, so they are refused
// outside development and test.
var devSecrets = map[string]string{
	"OTP_SECRET":           "local-development-otp-secret-not-for-real-use",
	"TOKEN_SIGNING_SECRET": "local-development-token-signing-secret-not-for-real-use",
	"MEDIA_SIGNING_SECRET": "local-development-media-signing-secret-not-for-real-use",
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
	if c.Database.MaxConns < 2 {
		errs = append(errs, errors.New("DATABASE_MAX_CONNS must be at least 2"))
	}
	// Slices, not maps, so the errors come out in the same order every time.
	for _, t := range []struct {
		key   string
		value time.Duration
	}{
		{"HTTP_READ_HEADER_TIMEOUT", c.HTTP.ReadHeaderTimeout},
		{"HTTP_READ_TIMEOUT", c.HTTP.ReadTimeout},
		{"HTTP_WRITE_TIMEOUT", c.HTTP.WriteTimeout},
		{"HTTP_IDLE_TIMEOUT", c.HTTP.IdleTimeout},
		{"HTTP_SHUTDOWN_TIMEOUT", c.HTTP.ShutdownTimeout},
		{"WORKER_SHUTDOWN_TIMEOUT", c.Worker.StopTimeout},
		{"DATABASE_STATEMENT_TIMEOUT", c.Database.StatementTimeout},
		{"DATABASE_LOCK_TIMEOUT", c.Database.LockTimeout},
		{"DATABASE_IDLE_IN_TRANSACTION_SESSION_TIMEOUT", c.Database.IdleInTxTimeout},
		{"FCM_TIMEOUT", c.Push.FCMTimeout},
	} {
		if t.value <= 0 {
			errs = append(errs, fmt.Errorf("%s must be positive", t.key))
		}
	}
	if len(c.Auth.OTPSecret) < 32 {
		errs = append(errs, errors.New("OTP_SECRET must be at least 32 bytes"))
	}
	if len(c.Auth.TokenSecret) < 32 {
		errs = append(errs, errors.New("TOKEN_SIGNING_SECRET must be at least 32 bytes"))
	}
	if len(c.Media.SigningSecret) < 32 {
		errs = append(errs, errors.New("MEDIA_SIGNING_SECRET must be at least 32 bytes"))
	}
	if c.Env == EnvStaging || c.Env == EnvProduction {
		if !filepath.IsAbs(c.Media.Dir) {
			errs = append(errs, errors.New("MEDIA_DIR must be an absolute path outside development"))
		}
		for _, s := range []struct{ key, value string }{
			{"OTP_SECRET", c.Auth.OTPSecret},
			{"TOKEN_SIGNING_SECRET", c.Auth.TokenSecret},
			{"MEDIA_SIGNING_SECRET", c.Media.SigningSecret},
		} {
			if s.value == devSecrets[s.key] {
				errs = append(errs, fmt.Errorf("%s is the public development value; set a real secret", s.key))
			}
		}
	}
	if c.Auth.SMSProvider != "console" {
		errs = append(errs, errors.New("SMS_PROVIDER must be console (the only provider so far)"))
	}
	if c.Env == EnvProduction && c.Auth.SMSProvider == "console" {
		errs = append(errs, errors.New("SMS_PROVIDER=console prints login codes to the log and is not allowed in production"))
	}
	switch c.Push.Provider {
	case "console":
	case "fcm":
		if !filepath.IsAbs(c.Push.FCMCredentialsFile) {
			errs = append(errs, errors.New("FCM_CREDENTIALS_FILE must be the absolute path of the service account key when PUSH_PROVIDER=fcm"))
		}
	default:
		errs = append(errs, errors.New("PUSH_PROVIDER must be console or fcm"))
	}
	if c.Env == EnvProduction && c.Push.Provider == "console" {
		errs = append(errs, errors.New("PUSH_PROVIDER=console writes notifications to the log instead of sending them and is not allowed in production"))
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
// errors.AsType (Go 1.26) is Go's typed error check — like
// `err instanceof ParseError` in JavaScript, but it also looks through
// wrapped errors, and hands back the typed value.
func withEnvNames(err error) error {
	agg, ok := errors.AsType[env.AggregateError](err)
	if !ok {
		return err
	}
	keys := envKeys(reflect.TypeFor[Config]())
	errs := make([]error, 0, len(agg.Errors))
	for _, e := range agg.Errors {
		if pe, ok := errors.AsType[env.ParseError](e); ok && keys[pe.Name] != "" {
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
