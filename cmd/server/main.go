// Command server is the barbershop backend. One binary, several roles:
//
//	server api       serve the HTTP API (default)
//	server migrate   apply pending database migrations, then exit
//
// The worker role (background jobs) arrives with River in M3.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	_ "time/tzdata" // branch time zones work even on an image without /usr/share/zoneinfo

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business"
	businesshttp "github.com/Abdulkhaliq-84/barbershop-backend/internal/business/adapters/httpapi"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/iam"
	iamhttp "github.com/Abdulkhaliq-84/barbershop-backend/internal/iam/adapters/httpapi"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/media"
	mediahttp "github.com/Abdulkhaliq-84/barbershop-backend/internal/media/adapters/httpapi"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/clock"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/config"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/database"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/httpx"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/logging"
)

// version is stamped at build time by the Dockerfile and the Makefile:
//
//	go build -ldflags "-X main.version=v0.3.0" ./cmd/server
//
// A plain `go run` leaves it as "dev".
var version = "dev"

func main() {
	// main stays tiny: everything that can fail lives in run, which returns
	// an error instead of calling os.Exit, so it's testable and defers run.
	if err := run(context.Background(), os.Args[1:], os.Environ(), os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// run wires the application. Its inputs are explicit (args, environment,
// output) instead of globals — the whole dependency graph is visible here.
func run(ctx context.Context, args, environ []string, stdout io.Writer) error {
	// The context is cancelled on Ctrl-C or SIGTERM (docker stop, deploys);
	// everything below watches it to shut down cleanly.
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	role := "api"
	if len(args) > 0 {
		role = args[0]
	}
	if role != "api" && role != "migrate" {
		return errors.New("unknown command (want: api, migrate)")
	}

	cfg, err := config.Load(environ)
	if err != nil {
		return err
	}
	logger := logging.New(stdout, cfg.Log, cfg.Env)
	logger.InfoContext(ctx, "starting", slog.String("role", role), slog.String("version", version))

	pool, err := database.Open(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer pool.Close()

	if role == "migrate" {
		return database.Migrate(ctx, pool, logger)
	}

	handler, err := newHandler(cfg, pool, logger)
	if err != nil {
		return err
	}
	return serveAPI(ctx, cfg.HTTP, handler, logger)
}

// apiServer is the whole API: one embedded handler set per module. Embedding
// promotes each module's methods, so together they satisfy the generated
// apigen.StrictServerInterface. New modules add a line here.
//
// Every module calls its type Handlers, and an embedded field is named after
// its type, so two of them would clash; the aliases give each a distinct name.
type (
	iamAPI      = iamhttp.Handlers
	businessAPI = businesshttp.Handlers
	mediaAPI    = mediahttp.Handlers
)

type apiServer struct {
	*iamAPI      // iam: /v1/auth/*, /v1/me
	*businessAPI // business: /v1/businesses/* (incl. branches, staff), /v1/invitations/accept, /v1/me/memberships
	*mediaAPI    // media: /v1/media/* (signed downloads)
}

// newHandler builds every module and mounts the API next to the health checks.
func newHandler(cfg config.Config, pool *pgxpool.Pool, logger *slog.Logger) (http.Handler, error) {
	iamModule, err := iam.New(iam.Deps{
		Pool:        pool,
		Clock:       clock.System{},
		Logger:      logger,
		OTPSecret:   []byte(cfg.Auth.OTPSecret),
		TokenSecret: []byte(cfg.Auth.TokenSecret),
	})
	if err != nil {
		return nil, err
	}

	mediaModule, err := media.New(media.Deps{
		Pool: pool, Clock: clock.System{}, Logger: logger,
		Dir: cfg.Media.Dir, Secret: []byte(cfg.Media.SigningSecret),
	})
	if err != nil {
		return nil, err
	}
	businessModule := business.New(business.Deps{Pool: pool, Clock: clock.System{}, Logger: logger, Media: mediaModule, Users: iamModule})

	router := httpx.NewRouter(logger, httpx.NewHealth(pool, logger))
	api := apiServer{iamModule.HTTP(), businessModule.HTTP(), mediaModule.HTTP()}
	if err := httpx.MountAPI(router, api, logger, iamModule.Authenticate); err != nil {
		return nil, err
	}
	return router, nil
}

func serveAPI(ctx context.Context, cfg config.HTTP, handler http.Handler, logger *slog.Logger) error {
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}

	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", cfg.Addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.Addr, err)
	}
	logger.InfoContext(ctx, "http server listening", slog.String("addr", ln.Addr().String()))

	return httpx.Serve(ctx, srv, ln, cfg.ShutdownTimeout, logger)
}
