package ipc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"
)

// Server is an HTTP server that listens on a Unix domain socket.
//
// It applies a /v1 route prefix, sets JSON Content-Type on every response, and
// returns SPEC §10.3 error envelopes for unmatched routes. On start it removes
// any stale socket file. On stop (context cancellation) it removes the socket.
type Server struct {
	socketPath string
	httpServer *http.Server
	logger     *slog.Logger
}

// NewServer creates a Server that will listen at socketPath.
//
// If handler is nil a default ServeMux with no application routes is used (the
// catch-all not_found handler is always installed). Callers that want to
// register routes should pass a *http.ServeMux populated with /v1/… handlers.
func NewServer(socketPath string, handler http.Handler) *Server {
	if handler == nil {
		handler = http.NewServeMux()
	}

	wrapped := jsonMiddleware(notFoundHandler(handler))

	return &Server{
		socketPath: socketPath,
		httpServer: &http.Server{
			Handler: wrapped,
		},
		logger: slog.Default().With("component", "ipc"),
	}
}

// Start listens on the Unix socket and serves requests until ctx is cancelled.
//
// It removes a stale socket before binding (SPEC §9.3) and removes the socket
// after shutdown. Start blocks until shutdown is complete and returns nil on a
// clean context cancellation.
func (s *Server) Start(ctx context.Context) error {
	// Remove stale socket if it exists.
	if err := removeStaleSocket(s.socketPath); err != nil {
		return fmt.Errorf("ipc: remove stale socket: %w", err)
	}

	ln, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return fmt.Errorf("ipc: listen: %w", err)
	}

	// Set socket permissions to 0660 (SPEC §20.3).
	if err := os.Chmod(s.socketPath, 0660); err != nil {
		ln.Close()
		return fmt.Errorf("ipc: chmod socket: %w", err)
	}

	s.logger.Info("IPC server listening", "socket", s.socketPath)

	// Shutdown goroutine: waits for ctx cancellation, then gracefully shuts
	// down the HTTP server.
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.httpServer.Shutdown(shutCtx); err != nil {
			s.logger.Error("IPC server shutdown error", "err", err)
		}
	}()

	// Serve blocks until the listener is closed (by Shutdown).
	err = s.httpServer.Serve(ln)

	// Wait for the shutdown goroutine to finish.
	<-shutdownDone

	// Clean up the socket file (SPEC §9.3).
	if rmErr := os.Remove(s.socketPath); rmErr != nil && !os.IsNotExist(rmErr) {
		s.logger.Warn("failed to remove socket", "path", s.socketPath, "err", rmErr)
	}

	// http.ErrServerClosed is the expected result of a graceful Shutdown.
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// removeStaleSocket removes a pre-existing socket file at path. If the file
// does not exist the call succeeds silently.
func removeStaleSocket(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}
	return os.Remove(path)
}

// jsonMiddleware wraps an http.Handler and sets Content-Type: application/json
// on every response.
func jsonMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		next.ServeHTTP(w, r)
	})
}

// notFoundHandler wraps an http.Handler so that any response Go's default mux
// would send as a 404 is replaced with a SPEC §10.3 not_found error envelope.
//
// Go's ServeMux writes its own "404 page not found\n" body for unmatched
// routes. We intercept this by using a sniffing ResponseWriter: if the inner
// handler writes a 404 with the default body, we replace it entirely.
func notFoundHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &sniffer{ResponseWriter: w}
		next.ServeHTTP(sw, r)

		// Flush the sniffed response.
		sw.flush()
	})
}

// writeNotFound writes a JSON not_found error envelope (SPEC §10.3).
func writeNotFound(w http.ResponseWriter) {
	apiErr := NewAPIError(ErrNotFound, "the requested resource was not found")
	w.WriteHeader(http.StatusNotFound)
	w.Write(EncodeError(apiErr))
}

// sniffer is an http.ResponseWriter that buffers the first Write call so we
// can detect and replace Go's default 404 text body with a JSON envelope.
type sniffer struct {
	http.ResponseWriter
	code    int
	buf     []byte
	flushed bool
}

func (s *sniffer) WriteHeader(code int) {
	s.code = code
	// Don't forward yet — wait until flush.
}

func (s *sniffer) Write(b []byte) (int, error) {
	if s.flushed {
		return s.ResponseWriter.Write(b)
	}
	// Buffer the first write.
	s.buf = append(s.buf, b...)
	return len(b), nil
}

// flush writes the buffered response. If the status is 404 and the body is
// Go's default text, it replaces the response with a JSON error envelope.
func (s *sniffer) flush() {
	if s.flushed {
		return
	}
	s.flushed = true

	code := s.code
	if code == 0 {
		code = http.StatusOK
	}

	// Detect Go's default 404 body.
	if code == http.StatusNotFound && isDefault404Body(s.buf) {
		writeNotFound(s.ResponseWriter)
		return
	}

	s.ResponseWriter.WriteHeader(code)
	if len(s.buf) > 0 {
		s.ResponseWriter.Write(s.buf)
	}
}

// isDefault404Body returns true if body matches Go's standard library default
// 404 response.
func isDefault404Body(body []byte) bool {
	return string(body) == "404 page not found\n" || len(body) == 0
}
