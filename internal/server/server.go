package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
	userpb "github.com/hitesh22rana/chronoverse/pkg/proto/go/users"
	workflowpb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"

	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	"github.com/hitesh22rana/chronoverse/internal/pkg/crypto"
	"github.com/hitesh22rana/chronoverse/internal/pkg/redis"
)

// Server implements the HTTP server.
type Server struct {
	auth            auth.IAuth
	crypto          *crypto.Crypto
	rdb             *redis.Store
	usersClient     userpb.UsersServiceClient
	workflowsClient workflowpb.WorkflowsServiceClient
	jobsClient      jobspb.JobsServiceClient
	httpServer      *http.Server
	validationCfg   *ValidationConfig
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
func New(
	cfg *Config,
	auth auth.IAuth,
	crypto *crypto.Crypto,
	rdb *redis.Store,
	usersClient userpb.UsersServiceClient,
	workflowsClient workflowpb.WorkflowsServiceClient,
	jobsClient jobspb.JobsServiceClient,
) *Server {
	srv := &Server{
		auth:            auth,
		crypto:          crypto,
		rdb:             rdb,
		usersClient:     usersClient,
		workflowsClient: workflowsClient,
		jobsClient:      jobsClient,
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
		"/auth/register",
		s.withAllowedMethodMiddleware(
			http.MethodPost,
			withAttachBasicMetadataHeaderMiddleware(
				s.handleRegisterUser,
			),
		),
	)
	router.HandleFunc(
		"/auth/login",
		s.withAllowedMethodMiddleware(
			http.MethodPost,
			withAttachBasicMetadataHeaderMiddleware(
				s.handleLoginUser,
			),
		),
	)
	router.HandleFunc(
		"/auth/logout",
		s.withAllowedMethodMiddleware(
			http.MethodPost,
			s.withVerifyCSRFMiddleware(
				s.withVerifySessionMiddleware(
					withAttachBasicMetadataHeaderMiddleware(
						s.handleLogout,
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
					withAttachBasicMetadataHeaderMiddleware(
						s.handleValidate,
					),
				),
			),
		),
	)
	router.HandleFunc(
		"/workflows",
		func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				s.withAllowedMethodMiddleware(
					http.MethodGet,
					s.withVerifySessionMiddleware(
						withAttachBasicMetadataHeaderMiddleware(
							s.withAttachAuthorizationTokenInMetadataHeaderMiddleware(
								s.handleListWorkflows,
							),
						),
					),
				).ServeHTTP(w, r)
			case http.MethodPost:
				s.withAllowedMethodMiddleware(
					http.MethodPost,
					s.withVerifyCSRFMiddleware(
						s.withVerifySessionMiddleware(
							withAttachBasicMetadataHeaderMiddleware(
								s.withAttachAuthorizationTokenInMetadataHeaderMiddleware(
									s.handleCreateWorkflow,
								),
							),
						),
					),
				).ServeHTTP(w, r)
			default:
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
		})
	router.HandleFunc(
		"/workflows/{workflow_id}",
		func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				s.withAllowedMethodMiddleware(
					http.MethodGet,
					s.withVerifySessionMiddleware(
						withAttachBasicMetadataHeaderMiddleware(
							s.withAttachAuthorizationTokenInMetadataHeaderMiddleware(
								s.handleGetWorkflow,
							),
						),
					),
				).ServeHTTP(w, r)
			case http.MethodPut:
				s.withAllowedMethodMiddleware(
					http.MethodPut,
					s.withVerifyCSRFMiddleware(
						s.withVerifySessionMiddleware(
							withAttachBasicMetadataHeaderMiddleware(
								s.withAttachAuthorizationTokenInMetadataHeaderMiddleware(
									s.handleUpdateWorkflow,
								),
							),
						),
					),
				).ServeHTTP(w, r)
			case http.MethodDelete:
				s.withAllowedMethodMiddleware(
					http.MethodDelete,
					s.withVerifyCSRFMiddleware(
						s.withVerifySessionMiddleware(
							withAttachBasicMetadataHeaderMiddleware(
								s.withAttachAuthorizationTokenInMetadataHeaderMiddleware(
									s.handleTerminateWorkflow,
								),
							),
						),
					),
				).ServeHTTP(w, r)
			default:
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
		},
	)
	router.HandleFunc(
		"/workflows/{workflow_id}/jobs",
		s.withAllowedMethodMiddleware(
			http.MethodGet,
			s.withVerifySessionMiddleware(
				withAttachBasicMetadataHeaderMiddleware(
					s.withAttachAuthorizationTokenInMetadataHeaderMiddleware(
						s.handleListJobs,
					),
				),
			),
		),
	)
	router.HandleFunc(
		"/workflows/{workflow_id}/jobs/{job_id}",
		s.withAllowedMethodMiddleware(
			http.MethodGet,
			s.withVerifySessionMiddleware(
				withAttachBasicMetadataHeaderMiddleware(
					s.withAttachAuthorizationTokenInMetadataHeaderMiddleware(
						s.handleGetJob,
					),
				),
			),
		),
	)
	router.HandleFunc(
		"/workflows/{workflow_id}/jobs/{job_id}/logs",
		s.withAllowedMethodMiddleware(
			http.MethodGet,
			s.withVerifySessionMiddleware(
				withAttachBasicMetadataHeaderMiddleware(
					s.withAttachAuthorizationTokenInMetadataHeaderMiddleware(
						s.handleGetJobLogs,
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
