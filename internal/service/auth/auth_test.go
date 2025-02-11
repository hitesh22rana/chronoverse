package auth_test

import (
	"context"
	"testing"

	"github.com/go-playground/validator/v10"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hitesh22rana/chronoverse/internal/service/auth"
	authmock "github.com/hitesh22rana/chronoverse/internal/service/auth/mock"
)

func TestRegister(t *testing.T) {
	ctrl := gomock.NewController(t)

	// Create a mock repository
	repo := authmock.NewMockRepository(ctrl)

	// Create a new service
	s := auth.New(validator.New(), repo)

	type args struct {
		email    string
		password string
	}

	type want struct {
		userID string
		pat    string
	}

	// Test cases
	tests := []struct {
		name  string
		args  args
		mock  func(a args)
		want  want
		isErr bool
	}{
		{
			name: "success",
			args: args{
				email:    "test@gamil.com",
				password: "password12345",
			},
			mock: func(a args) {
				repo.EXPECT().Register(
					gomock.Any(),
					a.email,
					a.password,
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
			args: args{
				email:    "test",
				password: "password12345",
			},
			mock:  func(_ args) {},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: password too short",
			args: args{
				email:    "test@gamil.com",
				password: "pass",
			},
			mock:  func(_ args) {},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: already exists",
			args: args{
				email:    "test@gamil.com",
				password: "password12345",
			},
			mock: func(a args) {
				repo.EXPECT().Register(
					gomock.Any(),
					a.email,
					a.password,
				).Return("", "", status.Errorf(codes.AlreadyExists, "user already exists"))
			},
			want:  want{},
			isErr: true,
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		tt.mock(tt.args)
		t.Run(tt.name, func(t *testing.T) {
			userID, pat, err := s.Register(
				context.Background(),
				tt.args.email,
				tt.args.password,
			)

			if (err != nil) != tt.isErr {
				t.Errorf("Register() error = %v, wantErr %v", err, tt.isErr)
				return
			}

			if userID != tt.want.userID {
				t.Errorf("Register() userID = %v, want %v", userID, tt.want.userID)
			}
			if pat != tt.want.pat {
				t.Errorf("Register() pat = %v, want %v", pat, tt.want.pat)
			}
		})
	}
}

func TestLogin(t *testing.T) {
	ctrl := gomock.NewController(t)

	// Create a mock repository
	repo := authmock.NewMockRepository(ctrl)

	// Create a new service
	s := auth.New(validator.New(), repo)

	type args struct {
		email    string
		password string
	}

	type want struct {
		userID string
		pat    string
	}

	// Test cases
	tests := []struct {
		name  string
		args  args
		mock  func(a args)
		want  want
		isErr bool
	}{
		{
			name: "success",
			args: args{
				email:    "test@gamil.com",
				password: "password12345",
			},
			mock: func(a args) {
				repo.EXPECT().Login(
					gomock.Any(),
					a.email,
					a.password,
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
			args: args{
				email:    "test",
				password: "password12345",
			},
			mock:  func(_ args) {},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: password too short",
			args: args{
				email:    "test@gamil.com",
				password: "pass",
			},
			mock:  func(_ args) {},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: user not found",
			args: args{
				email:    "test@gamil.com",
				password: "password12345",
			},
			mock: func(a args) {
				repo.EXPECT().Login(
					gomock.Any(),
					a.email,
					a.password,
				).Return("", "", status.Errorf(codes.NotFound, "user not found"))
			},
			want:  want{},
			isErr: true,
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		tt.mock(tt.args)
		t.Run(tt.name, func(t *testing.T) {
			userID, pat, err := s.Login(
				context.Background(),
				tt.args.email,
				tt.args.password,
			)

			if (err != nil) != tt.isErr {
				t.Errorf("Login() error = %v, wantErr %v", err, tt.isErr)
				return
			}

			if userID != tt.want.userID {
				t.Errorf("Login() userID = %v, want %v", userID, tt.want.userID)
			}
			if pat != tt.want.pat {
				t.Errorf("Login() pat = %v, want %v", pat, tt.want.pat)
			}
		})
	}
}

func TestLogout(t *testing.T) {
	ctrl := gomock.NewController(t)

	// Create a mock repository
	repo := authmock.NewMockRepository(ctrl)

	// Create a new service
	s := auth.New(validator.New(), repo)

	type want struct {
		userID string
	}

	// Test cases
	tests := []struct {
		name  string
		mock  func()
		want  want
		isErr bool
	}{
		{
			name: "success",
			mock: func() {
				repo.EXPECT().Logout(gomock.Any()).Return("1", nil)
			},
			want: want{
				userID: "1",
			},
			isErr: false,
		},
		{
			name:  "error: invalid token",
			mock:  func() {},
			want:  want{},
			isErr: true,
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		tt.mock()
		t.Run(tt.name, func(t *testing.T) {
			userID, err := s.Logout(context.Background())

			if (err != nil) != tt.isErr {
				t.Errorf("Logout() error = %v, wantErr %v", err, tt.isErr)
				return
			}

			if userID != tt.want.userID {
				t.Errorf("Logout() userID = %v, want %v", userID, tt.want.userID)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	ctrl := gomock.NewController(t)

	// Create a mock repository
	repo := authmock.NewMockRepository(ctrl)

	// Create a new service
	s := auth.New(validator.New(), repo)

	type want struct {
		userID string
	}

	// Test cases
	tests := []struct {
		name  string
		mock  func()
		want  want
		isErr bool
	}{
		{
			name: "success",
			mock: func() {
				repo.EXPECT().Validate(
					gomock.Any(),
				).Return("1", nil)
			},
			want: want{
				userID: "1",
			},
			isErr: false,
		},
		{
			name:  "error: invalid token",
			mock:  func() {},
			want:  want{},
			isErr: true,
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		tt.mock()
		t.Run(tt.name, func(t *testing.T) {
			userID, err := s.Validate(context.Background())

			if (err != nil) != tt.isErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.isErr)
				return
			}

			if userID != tt.want.userID {
				t.Errorf("Validate() userID = %v, want %v", userID, tt.want.userID)
			}
		})
	}
}
