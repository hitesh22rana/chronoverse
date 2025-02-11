package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	pb "github.com/hitesh22rana/chronoverse/pkg/proto/go"
)

const (
	defaultShutdownTimeout = 10 * time.Second
)

// Server implements the HTTP server.
type Server struct {
	client           pb.AuthServiceClient
	httpServer       *http.Server
	requestBodyLimit int64
}

// Config represents the configuration of the HTTP server.
type Config struct {
	Host              string
	Port              int
	RequestTimeout    time.Duration
	ReadTimeout       time.Duration
	ReadHeaderTimeout time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	RequestBodyLimit  int64
}

// New creates a new HTTP server.
func New(cfg *Config, client pb.AuthServiceClient) *Server {
	srv := &Server{
		client: client,
		httpServer: &http.Server{
			Addr:              fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
			ReadTimeout:       cfg.ReadTimeout,
			ReadHeaderTimeout: cfg.ReadHeaderTimeout,
			WriteTimeout:      cfg.WriteTimeout,
			IdleTimeout:       cfg.IdleTimeout,
		},
		requestBodyLimit: cfg.RequestBodyLimit,
	}

	router := http.NewServeMux()
	srv.registerRoutes(router)
	srv.httpServer.Handler = router
	return srv
}

// registerRoutes registers the HTTP routes.
func (s *Server) registerRoutes(router *http.ServeMux) {
	router.HandleFunc(
		"/healthz",
		withMethodMiddleware(
			http.MethodGet,
			s.handleHealthz,
		),
	)
	router.HandleFunc(
		"/auth/register",
		withMethodMiddleware(
			http.MethodPost,
			withRequestBodyLimitMiddleware(
				s.requestBodyLimit,
				withAttachAudienceInContextMiddleware(
					s.handleRegister,
				),
			),
		),
	)
	router.HandleFunc(
		"/auth/login",
		withMethodMiddleware(
			http.MethodPost,
			withRequestBodyLimitMiddleware(
				s.requestBodyLimit,
				withAttachAudienceInContextMiddleware(
					s.handleLogin,
				),
			),
		),
	)
	router.HandleFunc(
		"/auth/logout",
		withMethodMiddleware(
			http.MethodPost,
			withRequestBodyLimitMiddleware(
				s.requestBodyLimit,
				withVerifyCSRFMiddleware(
					withAttachPatInContextMiddleware(
						withAttachAudienceInContextMiddleware(
							s.handleLogout,
						),
					),
				),
			),
		),
	)
	router.HandleFunc(
		"/auth/validate",
		withMethodMiddleware(
			http.MethodPost,
			withRequestBodyLimitMiddleware(
				s.requestBodyLimit,
				withVerifyCSRFMiddleware(
					withAttachPatInContextMiddleware(
						withAttachAudienceInContextMiddleware(
							s.handleValidate,
						),
					),
				),
			),
		),
	)
}

// Start starts the HTTP server.
func (s *Server) Start() error {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "Failed to start server: %v\n", err)
			os.Exit(1)
		}
	}()

	sig := <-sigChan
	fmt.Fprintf(os.Stdout, "Received signal: %v\n", sig)

	ctx, cancel := context.WithTimeout(context.Background(), defaultShutdownTimeout)
	defer cancel()

	if err := s.httpServer.Shutdown(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Server shutdown failed: %v\n", err)
		return err
	}

	fmt.Fprintf(os.Stdout, "Server gracefully stopped")
	return nil
}
