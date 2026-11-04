package kit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// Server wraps an HTTP server with graceful shutdown and standard lifecycle.
type Server struct {
	httpServer *http.Server
	logger     *slog.Logger
	addr       string
}

// NewServer creates an HTTP server that listens on addr and handles graceful shutdown.
func NewServer(addr string, handler http.Handler, logger *slog.Logger) *Server {
	return &Server{
		httpServer: &http.Server{
			Addr:              addr,
			Handler:           handler,
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      60 * time.Second,
			IdleTimeout:       120 * time.Second,
		},
		logger: logger,
		addr:   addr,
	}
}

// Start begins listening and returns immediately. It blocks waiting for OS
// signals, then performs graceful shutdown.
func (s *Server) Start() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		s.logger.Info("http server listening", "address", s.addr)
		if err := s.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.logger.Error("http server error", "error", err)
			stop()
		}
	}()

	<-ctx.Done()
	s.logger.Info("shutting down http server", "address", s.addr)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	return nil
}
