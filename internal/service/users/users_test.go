package users_test

import (
	"testing"

	"github.com/go-playground/validator/v10"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/service/users"
	usersmock "github.com/hitesh22rana/chronoverse/internal/service/users/mock"
	userpb "github.com/hitesh22rana/chronoverse/pkg/proto/go/users"
)

func TestRegisterUser(t *testing.T) {
	ctrl := gomock.NewController(t)

	// Create a mock repository
	repo := usersmock.NewMockRepository(ctrl)

	// Create a new service
	s := users.New(validator.New(), repo)

	type want struct {
		userID string
		pat    string
	}

	// Test cases
	tests := []struct {
		name  string
		req   *userpb.RegisterUserRequest
		mock  func(req *userpb.RegisterUserRequest)
		want  want
		isErr bool
	}{
		{
			name: "success",
			req: &userpb.RegisterUserRequest{
				Email:    "test@gmail.com",
				Password: "password12345",
			},
			mock: func(req *userpb.RegisterUserRequest) {
				repo.EXPECT().RegisterUser(
					gomock.Any(),
					req.GetEmail(),
					req.GetPassword(),
				).Return("1", "token", nil)
			},
			want: want{
				userID: "1",
				pat:    "token",
			},
			isErr: false,
		},
		{
			name: "error: invalid email",
			req: &userpb.RegisterUserRequest{
				Email:    "test",
				Password: "password12345",
			},
			mock:  func(_ *userpb.RegisterUserRequest) {},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: password too short",
			req: &userpb.RegisterUserRequest{
				Email:    "test@gmail.com",
				Password: "pass",
			},
			mock:  func(_ *userpb.RegisterUserRequest) {},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: already exists",
			req: &userpb.RegisterUserRequest{
				Email:    "test@gmail.com",
				Password: "password12345",
			},
			mock: func(req *userpb.RegisterUserRequest) {
				repo.EXPECT().RegisterUser(
					gomock.Any(),
					req.GetEmail(),
					req.GetPassword(),
				).Return("", "", status.Errorf(codes.AlreadyExists, "user already exists"))
			},
			want:  want{},
			isErr: true,
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		tt.mock(tt.req)
		t.Run(tt.name, func(t *testing.T) {
			userID, pat, err := s.RegisterUser(t.Context(), tt.req)

			if (err != nil) != tt.isErr {
				t.Errorf("RegisterUser() error = %v, wantErr %v", err, tt.isErr)
				return
			}

			if userID != tt.want.userID {
				t.Errorf("RegisterUser() userID = %v, want %v", userID, tt.want.userID)
			}
			if pat != tt.want.pat {
				t.Errorf("RegisterUser() pat = %v, want %v", pat, tt.want.pat)
			}
		})
	}
}

func TestLoginUser(t *testing.T) {
	ctrl := gomock.NewController(t)

	// Create a mock repository
	repo := usersmock.NewMockRepository(ctrl)

	// Create a new service
	s := users.New(validator.New(), repo)

	type want struct {
		userID string
		pat    string
	}

	// Test cases
	tests := []struct {
		name  string
		req   *userpb.LoginUserRequest
		mock  func(req *userpb.LoginUserRequest)
		want  want
		isErr bool
	}{
		{
			name: "success",
			req: &userpb.LoginUserRequest{
				Email:    "test@gmail.com",
				Password: "password12345",
			},
			mock: func(req *userpb.LoginUserRequest) {
				repo.EXPECT().LoginUser(
					gomock.Any(),
					req.GetEmail(),
					req.GetPassword(),
				).Return("1", "token", nil)
			},
			want: want{
				userID: "1",
				pat:    "token",
			},
			isErr: false,
		},
		{
			name: "error: invalid email",
			req: &userpb.LoginUserRequest{
				Email:    "test@",
				Password: "password12345",
			},
			mock:  func(_ *userpb.LoginUserRequest) {},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: password too short",
			req: &userpb.LoginUserRequest{
				Email:    "test@gmail.com",
				Password: "pass",
			},
			mock:  func(_ *userpb.LoginUserRequest) {},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: user not found",
			req: &userpb.LoginUserRequest{
				Email:    "test@gmail.com",
				Password: "password12345",
			},
			mock: func(req *userpb.LoginUserRequest) {
				repo.EXPECT().LoginUser(
					gomock.Any(),
					req.GetEmail(),
					req.GetPassword(),
				).Return("", "", status.Errorf(codes.NotFound, "user not found"))
			},
			want:  want{},
			isErr: true,
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		tt.mock(tt.req)
		t.Run(tt.name, func(t *testing.T) {
			userID, pat, err := s.LoginUser(t.Context(), tt.req)

			if (err != nil) != tt.isErr {
				t.Errorf("LoginUser() error = %v, wantErr %v", err, tt.isErr)
				return
			}

			if userID != tt.want.userID {
				t.Errorf("LoginUser() userID = %v, want %v", userID, tt.want.userID)
			}
			if pat != tt.want.pat {
				t.Errorf("LoginUser() pat = %v, want %v", pat, tt.want.pat)
			}
		})
	}
}
