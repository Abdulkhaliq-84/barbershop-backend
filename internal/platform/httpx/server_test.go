package httpx_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/httpx"
)

func TestServeShutsDownGracefully(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0") // :0 = any free port
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{
		Handler:           httpx.NewRouter(logger, httpx.NewHealth(fakePinger{}, logger)),
		ReadHeaderTimeout: time.Second,
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- httpx.Serve(ctx, srv, ln, 5*time.Second, logger) }()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+ln.Addr().String()+"/healthz", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	cancel() // what SIGTERM does in main

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve() error = %v, want nil after graceful shutdown", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve() did not return after the context was cancelled")
	}
}
