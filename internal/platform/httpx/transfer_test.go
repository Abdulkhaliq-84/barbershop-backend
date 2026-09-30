package httpx_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/apigen"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/auth"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/httpx"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// fileServer implements the two file operations; any other call panics on
// the nil embedded interface.
type fileServer struct {
	apigen.StrictServerInterface
	upload   func(apigen.UploadVerificationDocumentRequestObject)
	download io.Reader
}

func (f fileServer) UploadVerificationDocument(_ context.Context, req apigen.UploadVerificationDocumentRequestObject) (apigen.UploadVerificationDocumentResponseObject, error) {
	f.upload(req)
	return apigen.UploadVerificationDocument201JSONResponse{}, nil
}

func (f fileServer) DownloadMedia(context.Context, apigen.DownloadMediaRequestObject) (apigen.DownloadMediaResponseObject, error) {
	return apigen.DownloadMedia200ApplicationoctetStreamResponse{Body: f.download}, nil
}

func mountFiles(t *testing.T, logs io.Writer, server fileServer) http.Handler {
	t.Helper()
	logger := slog.New(slog.NewJSONHandler(logs, nil))
	router := httpx.NewRouter(logger, httpx.NewHealth(fakePinger{}, logger))
	signedIn := func(context.Context, string) (auth.Principal, error) {
		return auth.Principal{UserID: shared.NewID[shared.UserTag]()}, nil
	}
	if err := httpx.MountAPI(router, server, logger, signedIn); err != nil {
		t.Fatal(err)
	}
	return router
}

// countingReader counts what has been read from it.
type countingReader struct {
	r io.Reader
	n int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}

// The validator reads a body whole to check it; an upload's must reach the
// handler untouched, to be streamed.
func TestUploadBodyIsStreamedNotBuffered(t *testing.T) {
	t.Parallel()
	file := &countingReader{r: bytes.NewReader(make([]byte, 4<<20))}
	readBefore := -1
	router := mountFiles(t, io.Discard, fileServer{upload: func(req apigen.UploadVerificationDocumentRequestObject) {
		readBefore = file.n
		_, _ = io.Copy(io.Discard, req.Body)
	}})
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/v1/businesses/"+shared.NewID[shared.BusinessTag]().String()+"/verification/documents?kind=cr_certificate", file)
	req.Header.Set("Authorization", "Bearer x")
	req.Header.Set("Content-Type", "application/octet-stream")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated || readBefore != 0 || file.n != 4<<20 {
		t.Fatalf("status %d; %d bytes read before the handler, %d in all: %s", rec.Code, readBefore, file.n, rec.Body)
	}
	// The query is still validated.
	req = httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/v1/businesses/"+shared.NewID[shared.BusinessTag]().String()+"/verification/documents?kind=selfie", strings.NewReader("x"))
	req.Header.Set("Authorization", "Bearer x")
	req.Header.Set("Content-Type", "application/octet-stream")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("unknown kind: %d %s", rec.Code, rec.Body)
	}
	// And so is the Content-Type, though the body isn't: JSON is not a file.
	file = &countingReader{r: strings.NewReader(`{}`)}
	req = httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/v1/businesses/"+shared.NewID[shared.BusinessTag]().String()+"/verification/documents?kind=cr_certificate", file)
	req.Header.Set("Authorization", "Bearer x")
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || file.n != 0 {
		t.Errorf("json upload: %d %s, %d bytes read", rec.Code, rec.Body, file.n)
	}
}

// A JSON operation keeps its 1 MiB cap and its body checks whatever
// Content-Type the client claims.
func TestUploadRulesFollowTheRouteNotTheContentType(t *testing.T) {
	t.Parallel()
	router := mountFiles(t, io.Discard, fileServer{})
	for name, tt := range map[string]struct {
		body   []byte
		status int
	}{
		"2 MiB":       {make([]byte, 2<<20), http.StatusRequestEntityTooLarge},
		"a small one": {[]byte("not json"), http.StatusBadRequest},
	} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/businesses", bytes.NewReader(tt.body))
		req.Header.Set("Authorization", "Bearer x")
		req.Header.Set("Content-Type", "application/octet-stream")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != tt.status {
			t.Errorf("%s: %d %s, want %d", name, rec.Code, rec.Body, tt.status)
		}
	}
}

// failingReader gives some bytes, then fails, like a file whose client went
// away mid-download.
type failingReader struct{ sent bool }

func (f *failingReader) Read(p []byte) (int, error) {
	if f.sent {
		return 0, errors.New("connection reset")
	}
	f.sent = true
	return copy(p, "%PDF-1.7 partial"), nil
}

// Once a file has started going out, a failure can't become a problem
// response: the status is sent, and JSON would be glued onto the file.
func TestNoProblemAfterTheResponseStarted(t *testing.T) {
	t.Parallel()
	var logs strings.Builder
	router := mountFiles(t, &logs, fileServer{download: &failingReader{}})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/v1/media/"+shared.NewID[shared.MediaTag]().String()+"?expires=1&signature=x", nil))

	if rec.Code != http.StatusOK || rec.Body.String() != "%PDF-1.7 partial" {
		t.Fatalf("status %d, body %q", rec.Code, rec.Body)
	}
	if out := logs.String(); !strings.Contains(out, `"msg":"api response cut short"`) || strings.Contains(out, `"level":"ERROR"`) {
		t.Errorf("logs: %s", out)
	}
}

// A panic after the response started aborts the connection rather than
// writing a second status.
func TestRecoverAfterTheResponseStartedAborts(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.DiscardHandler)
	router := httpx.NewRouter(logger, httpx.NewHealth(fakePinger{}, logger))
	router.Get("/half", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("half"))
		panic("kaboom")
	})
	defer func() {
		if rec := recover(); rec != http.ErrAbortHandler { //nolint:errorlint // the sentinel itself is re-panicked
			t.Errorf("recovered %v, want http.ErrAbortHandler", rec)
		}
	}()
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/half", nil))
	t.Error("the handler didn't abort")
}

// Uploads are still refused without a token, before any of the file is read.
func TestUploadStillNeedsAToken(t *testing.T) {
	t.Parallel()
	file := &countingReader{r: bytes.NewReader(make([]byte, 1<<20))}
	logger := slog.New(slog.DiscardHandler)
	router := httpx.NewRouter(logger, httpx.NewHealth(fakePinger{}, logger))
	nobody := func(context.Context, string) (auth.Principal, error) {
		return auth.Principal{}, errors.New("bad token")
	}
	if err := httpx.MountAPI(router, fileServer{}, logger, nobody); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/v1/businesses/"+shared.NewID[shared.BusinessTag]().String()+"/verification/documents?kind=cr_certificate", file)
	req.Header.Set("Content-Type", "application/octet-stream")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized || file.n != 0 {
		t.Fatalf("status %d, %d bytes read", rec.Code, file.n)
	}
}

// slowReader sends its bytes in pieces, pausing before each.
type slowReader struct {
	pieces int
	pause  time.Duration
}

func (s *slowReader) Read(p []byte) (int, error) {
	if s.pieces == 0 {
		return 0, io.EOF
	}
	s.pieces--
	time.Sleep(s.pause)
	return copy(p, "%PDF-1.7 "), nil
}

// A real server with 300 ms read and write timeouts: a file that takes a
// second to arrive or leave still makes it — the server-wide timeouts are
// for small JSON requests.
func TestTransfersOutliveTheServerTimeouts(t *testing.T) {
	t.Parallel()
	var uploaded int64
	srv := httptest.NewUnstartedServer(mountFiles(t, io.Discard, fileServer{
		upload: func(req apigen.UploadVerificationDocumentRequestObject) {
			uploaded, _ = io.Copy(io.Discard, req.Body)
		},
		download: &slowReader{pieces: 5, pause: 200 * time.Millisecond},
	}))
	srv.Config.ReadTimeout, srv.Config.WriteTimeout = 300*time.Millisecond, 300*time.Millisecond
	srv.Start()
	t.Cleanup(srv.Close)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		srv.URL+"/v1/businesses/"+shared.NewID[shared.BusinessTag]().String()+"/verification/documents?kind=cr_certificate",
		&slowReader{pieces: 5, pause: 200 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer x")
	req.Header.Set("Content-Type", "application/octet-stream")
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("slow upload: %v", err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated || uploaded != 45 {
		t.Errorf("slow upload: %d, %d bytes", res.StatusCode, uploaded)
	}

	req, err = http.NewRequestWithContext(t.Context(), http.MethodGet,
		srv.URL+"/v1/media/"+shared.NewID[shared.MediaTag]().String()+"?expires=1&signature=x", nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err = srv.Client().Do(req)
	if err != nil {
		t.Fatalf("slow download: %v", err)
	}
	body, err := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if err != nil || len(body) != 45 {
		t.Errorf("slow download: %d bytes, %v", len(body), err)
	}
}
