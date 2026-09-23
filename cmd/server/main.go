// Command server is the barbershop backend. One binary, several roles:
//
//	server api       serve the HTTP API (default)
//	server migrate   apply pending database migrations, then exit
//
// The worker role (background jobs) arrives with River in M3.
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/config"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/database"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/httpx"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/logging"
)

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
		return fmt.Errorf("unknown command %q (want: api, migrate)", role)
	}

	cfg, err := config.Load(environ)
	if err != nil {
		return err
	}
	logger := logging.New(stdout, cfg.Log, cfg.Env)

	pool, err := database.Open(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer pool.Close()

	if role == "migrate" {
		return database.Migrate(ctx, pool, logger)
	}
	return serveAPI(ctx, cfg.HTTP, pool, logger)
}

func serveAPI(ctx context.Context, cfg config.HTTP, db httpx.Pinger, logger *slog.Logger) error {
	srv := &http.Server{
		Handler:           httpx.NewRouter(logger, httpx.NewHealth(db, logger)),
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
