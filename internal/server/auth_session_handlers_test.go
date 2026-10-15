//nolint:testpackage // Exercises unexported HTTP authentication boundaries.
package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/pkg/crypto"
	userspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/users"
)

type sessionUsersClient struct {
	userspb.UsersServiceClient
	header   metadata.MD
	err      error
	calls    int
	register *userspb.RegisterUserRequest
	login    *userspb.LoginUserRequest
}

func (c *sessionUsersClient) reply(options []grpc.CallOption) {
	c.calls++
	for _, option := range options {
		if header, ok := option.(grpc.HeaderCallOption); ok {
			*header.HeaderAddr = c.header.Copy()
		}
	}
}

func (c *sessionUsersClient) RegisterUser(_ context.Context, request *userspb.RegisterUserRequest, options ...grpc.CallOption) (*userspb.RegisterUserResponse, error) {
	c.reply(options)
	c.register = request
	return &userspb.RegisterUserResponse{UserId: testSessionUserID}, c.err
}

func (c *sessionUsersClient) LoginUser(_ context.Context, request *userspb.LoginUserRequest, options ...grpc.CallOption) (*userspb.LoginUserResponse, error) {
	c.reply(options)
	c.login = request
	return &userspb.LoginUserResponse{UserId: testSessionUserID}, c.err
}

func authSessionRequest(register bool, body, contentType string) *http.Request {
	path := "/login"
	if register {
		path = "/register"
	}
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.Header.Set("Content-Type", contentType)
	if register {
		request.Header.Set(idempotencyKeyHeader, "registration-key")
	}
	return request
}

func invokeAuthSession(s *Server, register bool, response http.ResponseWriter, request *http.Request) {
	if register {
		s.handleRegisterUser(response, request)
	} else {
		s.handleLoginUser(response, request)
	}
}

func TestAuthSessionRequestGuards(t *testing.T) {
	for _, register := range []bool{false, true} {
		name := "login"
		if register {
			name = "register"
		}
		t.Run(name, func(t *testing.T) {
			for _, test := range []struct{ name, body, contentType string }{
				{"simple form", `email=a&password=b`, "application/x-www-form-urlencoded"},
				{"plain JSON", `{}`, "text/plain"},
				{"malformed JSON", `{`, testJSONContentType},
				{"duplicate email", `{"email":"a","email":"b"}`, testJSONContentType},
				{"trailing value", `{} {}`, testJSONContentType},
				{"wrong field type", `{"password":42}`, testJSONContentType},
			} {
				t.Run(test.name, func(t *testing.T) {
					client := &sessionUsersClient{}
					response := httptest.NewRecorder()
					invokeAuthSession(&Server{usersClient: client}, register, response, authSessionRequest(register, test.body, test.contentType))
					assertAuthSessionError(t, response, http.StatusBadRequest, "invalid request body\n")
					if client.calls != 0 {
						t.Fatal("invalid request reached users RPC")
					}
				})
			}
		})
	}
	t.Run("registration requires key before body", func(t *testing.T) {
		client := &sessionUsersClient{}
		request := authSessionRequest(true, `{`, testJSONContentType)
		request.Header.Del(idempotencyKeyHeader)
		response := httptest.NewRecorder()
		(&Server{usersClient: client}).handleRegisterUser(response, request)
		assertAuthSessionError(t, response, http.StatusBadRequest, "idempotency key is required\n")
		if client.calls != 0 {
			t.Fatal("missing key reached users RPC")
		}
	})
}

func assertAuthSessionError(t *testing.T, response *httptest.ResponseRecorder, wantStatus int, wantBody string) {
	t.Helper()
	if response.Code != wantStatus || response.Body.String() != wantBody {
		t.Fatalf("response = %d %q; want %d %q", response.Code, response.Body.String(), wantStatus, wantBody)
	}
	if cookies := response.Result().Cookies(); len(cookies) != 0 {
		t.Fatalf("failed authentication issued cookies: %v", cookies)
	}
}

func TestAuthSessionRPCFailures(t *testing.T) {
	for _, register := range []bool{false, true} {
		name := "login"
		if register {
			name = "register"
		}
		t.Run(name, func(t *testing.T) {
			for _, test := range []struct {
				code       codes.Code
				httpStatus int
			}{
				{codes.InvalidArgument, 400},
				{codes.Unauthenticated, 401},
				{codes.PermissionDenied, 403},
				{codes.AlreadyExists, 409},
				{codes.Aborted, 409},
				{codes.ResourceExhausted, 429},
				{codes.Unavailable, 503},
				{codes.DeadlineExceeded, 504},
				{codes.Canceled, 408},
				{codes.Internal, 500},
			} {
				t.Run(test.code.String(), func(t *testing.T) {
					client := &sessionUsersClient{err: status.Error(test.code, "backend validation detail")}
					response := httptest.NewRecorder()
					invokeAuthSession(&Server{usersClient: client}, register, response, authSessionRequest(register, `{"email":"alice@example.com","password":"secret"}`, testJSONContentType))
					message := "failed to login user\n"
					if register {
						message = "failed to register user\n"
						if test.code == codes.InvalidArgument {
							message = "backend validation detail\n"
						}
					}
					assertAuthSessionError(t, response, test.httpStatus, message)
					assertAuthSessionRPC(t, client, register)
				})
			}
		})
	}
}

func assertAuthSessionRPC(t *testing.T, client *sessionUsersClient, register bool) {
	t.Helper()
	if client.calls != 1 {
		t.Fatalf("RPC calls = %d, want 1", client.calls)
	}
	if register {
		if client.register.GetEmail() != "alice@example.com" || client.register.GetPassword() != "secret" || client.register.GetIdempotencyKey() != "registration-key" {
			t.Fatalf("registration request = %v", client.register)
		}
	} else if client.login.GetEmail() != "alice@example.com" || client.login.GetPassword() != "secret" {
		t.Fatalf("login request = %v", client.login)
	}
}

func TestAuthSessionRejectsMissingAuthorization(t *testing.T) {
	for _, register := range []bool{false, true} {
		name := "login"
		if register {
			name = "register"
		}
		t.Run(name, func(t *testing.T) {
			for _, header := range []metadata.MD{nil, metadata.Pairs("authorization", "Basic abc"), metadata.Pairs("authorization", "Bearer")} {
				client := &sessionUsersClient{header: header}
				response := httptest.NewRecorder()
				invokeAuthSession(&Server{usersClient: client}, register, response, authSessionRequest(register, `{"email":"alice@example.com","password":"secret"}`, testJSONContentType))
				assertAuthSessionError(t, response, http.StatusPreconditionFailed, "failed to get authorization token from headers\n")
				assertAuthSessionRPC(t, client, register)
			}
		})
	}
}

func TestAuthSessionRejectsUninitializedEncryption(t *testing.T) {
	for _, register := range []bool{false, true} {
		name := "login"
		if register {
			name = "register"
		}
		t.Run(name, func(t *testing.T) {
			client := &sessionUsersClient{header: metadata.Pairs("authorization", "Bearer private.jwt.token")}
			server := &Server{usersClient: client, crypto: &crypto.Crypto{}}
			response := httptest.NewRecorder()
			invokeAuthSession(server, register, response, authSessionRequest(register, `{"email":"alice@example.com","password":"secret"}`, testJSONContentType))
			assertAuthSessionError(t, response, http.StatusInternalServerError, "failed to encrypt session\n")
			assertAuthSessionRPC(t, client, register)
		})
	}
}
