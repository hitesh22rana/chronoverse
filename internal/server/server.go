package server

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	analyticspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/analytics"
	jobspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/jobs"
	notificationspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/notifications"
	userpb "github.com/hitesh22rana/chronoverse/pkg/proto/go/users"
	workflowpb "github.com/hitesh22rana/chronoverse/pkg/proto/go/workflows"

	"github.com/hitesh22rana/chronoverse/internal/pkg/auth"
	"github.com/hitesh22rana/chronoverse/internal/pkg/crypto"
	loggerpkg "github.com/hitesh22rana/chronoverse/internal/pkg/logger"
	"github.com/hitesh22rana/chronoverse/internal/pkg/redis"
	svcpkg "github.com/hitesh22rana/chronoverse/internal/pkg/svc"
)

// Server implements the HTTP server.
type Server struct {
	tp                  trace.Tracer
	logger              *zap.Logger
	auth                auth.IAuth
	crypto              *crypto.Crypto
	rdb                 *redis.Store
	usersClient         userpb.UsersServiceClient
	workflowsClient     workflowpb.WorkflowsServiceClient
	jobsClient          jobspb.JobsServiceClient
	notificationsClient notificationspb.NotificationsServiceClient
	analyticsClient     analyticspb.AnalyticsServiceClient
	httpServer          *http.Server
	validationCfg       *ValidationConfig
	frontendConfig      *FrontendConfig
}

// ValidationConfig represents the configuration of the validation.
type ValidationConfig struct {
	SessionExpiry    time.Duration
	CSRFExpiry       time.Duration
	RequestBodyLimit int64
	CSRFHMACSecret   string
}

// FrontendConfig represents the configuration of the frontend.
type FrontendConfig struct {
	URL    string
	Host   string
	Secure bool
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
	FrontendURL       string
}

// New creates a new HTTP server.
func New(
	ctx context.Context,
	cfg *Config,
	auth auth.IAuth,
	crypto *crypto.Crypto,
	rdb *redis.Store,
	usersClient userpb.UsersServiceClient,
	workflowsClient workflowpb.WorkflowsServiceClient,
	jobsClient jobspb.JobsServiceClient,
	notificationsClient notificationspb.NotificationsServiceClient,
	analyticsClient analyticspb.AnalyticsServiceClient,
) *Server {
	logger := loggerpkg.FromContext(ctx)
	frontend, err := url.Parse(cfg.FrontendURL)
	if err != nil {
		logger.Fatal("failed to parse frontend URL", zap.Error(err))
	}

	srv := &Server{
		tp:                  otel.Tracer(svcpkg.Info().GetName()),
		logger:              logger,
		auth:                auth,
		crypto:              crypto,
		rdb:                 rdb,
		usersClient:         usersClient,
		workflowsClient:     workflowsClient,
		jobsClient:          jobsClient,
		notificationsClient: notificationsClient,
		analyticsClient:     analyticsClient,
		httpServer: &http.Server{
			Addr:              fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
			ReadTimeout:       cfg.ReadTimeout,
			ReadHeaderTimeout: cfg.ReadHeaderTimeout,
			IdleTimeout:       cfg.IdleTimeout,
		},
		validationCfg: cfg.ValidationConfig,
		frontendConfig: &FrontendConfig{
			URL:    cfg.FrontendURL,
			Host:   frontend.Hostname(),
			Secure: frontend.Scheme == "https",
		},
	}

	router := http.NewServeMux()
	srv.registerRoutes(router)

	// Common middlewares
	srv.httpServer.Handler = srv.withOtelMiddleware(
		srv.withCORSMiddleware(
			srv.withCompressionMiddleware(router),
		),
	)
	return srv
}

// registerRoutes registers the HTTP routes.
func (s *Server) registerRoutes(router *http.ServeMux) {
	// Auth routes
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

	// Users routes
	router.HandleFunc(
		"/users",
		func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				s.withAllowedMethodMiddleware(
					http.MethodGet,
					s.withVerifySessionMiddleware(
						withAttachBasicMetadataHeaderMiddleware(
							s.withAttachAuthorizationTokenInMetadataHeaderMiddleware(
								s.handleGetUser,
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
									s.handleUpdateUser,
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

	// Workflows routes
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
		},
	)
	router.HandleFunc(
		"/workflows/test",
		s.withAllowedMethodMiddleware(
			http.MethodGet,
			s.withVerifyCSRFMiddleware(
				s.withVerifySessionMiddleware(
					withAttachBasicMetadataHeaderMiddleware(
						s.withAttachAuthorizationTokenInMetadataHeaderMiddleware(
							s.handleTestWorkflowRunEvents,
						),
					),
				),
			),
		),
	)
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
			case http.MethodPatch:
				s.withAllowedMethodMiddleware(
					http.MethodPatch,
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
			case http.MethodDelete:
				s.withAllowedMethodMiddleware(
					http.MethodDelete,
					s.withVerifyCSRFMiddleware(
						s.withVerifySessionMiddleware(
							withAttachBasicMetadataHeaderMiddleware(
								s.withAttachAuthorizationTokenInMetadataHeaderMiddleware(
									s.handleDeleteWorkflow,
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

	// Jobs routes
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
	router.HandleFunc(
		"/workflows/{workflow_id}/jobs/{job_id}/logs/raw",
		s.withAllowedMethodMiddleware(
			http.MethodGet,
			s.withVerifySessionMiddleware(
				withAttachBasicMetadataHeaderMiddleware(
					s.withAttachAuthorizationTokenInMetadataHeaderMiddleware(
						s.handleDownloadJobLogs,
					),
				),
			),
		),
	)
	router.HandleFunc(
		"/workflows/{workflow_id}/jobs/{job_id}/events",
		s.withAllowedMethodMiddleware(
			http.MethodGet,
			s.withVerifySessionMiddleware(
				withAttachBasicMetadataHeaderMiddleware(
					s.withAttachAuthorizationTokenInMetadataHeaderMiddleware(
						s.handleJobEvents,
					),
				),
			),
		),
	)

	// Notifications routes
	router.HandleFunc(
		"/notifications",
		func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				s.withAllowedMethodMiddleware(
					http.MethodGet,
					s.withVerifySessionMiddleware(
						withAttachBasicMetadataHeaderMiddleware(
							s.withAttachAuthorizationTokenInMetadataHeaderMiddleware(
								s.handleListNotifications,
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
									s.handleMarkNotificationsRead,
								),
							),
						),
					),
				).ServeHTTP(w, r)
			}
		},
	)

	// Analytics routes
	router.HandleFunc(
		"/analytics",
		s.withAllowedMethodMiddleware(
			http.MethodGet,
			s.withVerifySessionMiddleware(
				withAttachBasicMetadataHeaderMiddleware(
					s.withAttachAuthorizationTokenInMetadataHeaderMiddleware(
						s.handleGetUserAnalytics,
					),
				),
			),
		),
	)
	router.HandleFunc(
		"/analytics/{workflow_id}",
		s.withAllowedMethodMiddleware(
			http.MethodGet,
			s.withVerifySessionMiddleware(
				withAttachBasicMetadataHeaderMiddleware(
					s.withAttachAuthorizationTokenInMetadataHeaderMiddleware(
						s.handleGetWorkflowAnalytics,
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
		s.logger.Info("starting HTTP server",
			zap.String("addr", s.httpServer.Addr),
			zap.Bool("http_compression_enabled", true),
		)

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
