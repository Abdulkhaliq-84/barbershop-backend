package httpx

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// Serve runs srv on ln until ctx is cancelled (e.g. SIGTERM), then shuts it
// down gracefully: it stops accepting connections and waits up to
// shutdownTimeout for in-flight requests to finish.
//
// Node.js analogy: server.listen() + process.on('SIGTERM', () => server.close()).
// Here the signal arrives as a cancelled context instead of an event.
func Serve(ctx context.Context, srv *http.Server, ln net.Listener, shutdownTimeout time.Duration, logger *slog.Logger) error {
	serveErr := make(chan error, 1) // buffered: the goroutine never blocks, so it never leaks
	go func() {
		serveErr <- srv.Serve(ln)
	}()

	select {
	case err := <-serveErr:
		// Serve returned before we asked it to stop: that's a real failure.
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
	}

	logger.InfoContext(ctx, "shutting down http server", slog.Duration("timeout", shutdownTimeout))

	// ctx is already cancelled; derive a fresh deadline that keeps its values.
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}

	if err := <-serveErr; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("http server: %w", err)
	}
	logger.InfoContext(ctx, "http server stopped")
	return nil
}
