package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	"github.com/hitesh22rana/chronoverse/internal/pkg/crypto"
	"github.com/hitesh22rana/chronoverse/internal/pkg/redis"
	pb "github.com/hitesh22rana/chronoverse/pkg/proto/go"
)

// Server implements the HTTP server.
type Server struct {
	auth          *auth.Auth
	crypto        *crypto.Crypto
	rdb           *redis.Store
	usersClient   pb.UsersServiceClient
	httpServer    *http.Server
	validationCfg *ValidationConfig
}

// ValidationConfig represents the configuration of the validation.
type ValidationConfig struct {
	SessionExpiry    time.Duration
	CSRFExpiry       time.Duration
	RequestBodyLimit int64
	CSRFHMACSecret   string
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
	ValidationConfig  *ValidationConfig
}

// New creates a new HTTP server.
func New(cfg *Config, auth *auth.Auth, crypto *crypto.Crypto, rdb *redis.Store, usersClient pb.UsersServiceClient) *Server {
	srv := &Server{
		auth:        auth,
		crypto:      crypto,
		rdb:         rdb,
		usersClient: usersClient,
		httpServer: &http.Server{
			Addr:              fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
			ReadTimeout:       cfg.ReadTimeout,
			ReadHeaderTimeout: cfg.ReadHeaderTimeout,
			WriteTimeout:      cfg.WriteTimeout,
			IdleTimeout:       cfg.IdleTimeout,
		},
		validationCfg: cfg.ValidationConfig,
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
		s.withAllowedMethodMiddleware(
			http.MethodGet,
			s.handleHealthz,
		),
	)
	router.HandleFunc(
		"/auth/register",
		s.withAllowedMethodMiddleware(
			http.MethodPost,
			withAttachAudienceInMetadataHeaderMiddleware(
				withAttachRoleInMetadataHeaderMiddleware(
					s.handleRegister,
				),
			),
		),
	)
	router.HandleFunc(
		"/auth/login",
		s.withAllowedMethodMiddleware(
			http.MethodPost,
			withAttachAudienceInMetadataHeaderMiddleware(
				withAttachRoleInMetadataHeaderMiddleware(
					s.handleLogin,
				),
			),
		),
	)
	router.HandleFunc(
		"/auth/logout",
		s.withAllowedMethodMiddleware(
			http.MethodPost,
			s.withVerifyCSRFMiddleware(
				s.withVerifySessionMiddleware(
					withAttachAudienceInMetadataHeaderMiddleware(
						withAttachRoleInMetadataHeaderMiddleware(
							s.handleLogout,
						),
					),
				),
			),
		),
	)
	router.HandleFunc(
		"/auth/validate",
		s.withAllowedMethodMiddleware(
			http.MethodPost,
			s.withVerifyCSRFMiddleware(
				s.withVerifySessionMiddleware(
					withAttachAudienceInMetadataHeaderMiddleware(
						withAttachRoleInMetadataHeaderMiddleware(
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

	ctx, cancel := context.WithTimeout(context.Background(), serverShutdownTimeout)
	defer cancel()

	if err := s.httpServer.Shutdown(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Server shutdown failed: %v\n", err)
		return err
	}

	fmt.Fprintf(os.Stdout, "Server gracefully stopped")
	return nil
}
