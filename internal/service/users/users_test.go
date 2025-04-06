package users_test

import (
	"testing"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	usersmodel "github.com/hitesh22rana/chronoverse/internal/model/users"
	"github.com/hitesh22rana/chronoverse/internal/service/users"
	usersmock "github.com/hitesh22rana/chronoverse/internal/service/users/mock"
	userspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/users"
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
		req   *userspb.RegisterUserRequest
		mock  func(req *userspb.RegisterUserRequest)
		want  want
		isErr bool
	}{
		{
			name: "success",
			req: &userspb.RegisterUserRequest{
				Email:    "test@gmail.com",
				Password: "password12345",
			},
			mock: func(req *userspb.RegisterUserRequest) {
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
			req: &userspb.RegisterUserRequest{
				Email:    "test",
				Password: "password12345",
			},
			mock:  func(_ *userspb.RegisterUserRequest) {},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: password too short",
			req: &userspb.RegisterUserRequest{
				Email:    "test@gmail.com",
				Password: "pass",
			},
			mock:  func(_ *userspb.RegisterUserRequest) {},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: already exists",
			req: &userspb.RegisterUserRequest{
				Email:    "test@gmail.com",
				Password: "password12345",
			},
			mock: func(req *userspb.RegisterUserRequest) {
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
		req   *userspb.LoginUserRequest
		mock  func(req *userspb.LoginUserRequest)
		want  want
		isErr bool
	}{
		{
			name: "success",
			req: &userspb.LoginUserRequest{
				Email:    "test@gmail.com",
				Password: "password12345",
			},
			mock: func(req *userspb.LoginUserRequest) {
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
			req: &userspb.LoginUserRequest{
				Email:    "test@",
				Password: "password12345",
			},
			mock:  func(_ *userspb.LoginUserRequest) {},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: password too short",
			req: &userspb.LoginUserRequest{
				Email:    "test@gmail.com",
				Password: "pass",
			},
			mock:  func(_ *userspb.LoginUserRequest) {},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: user not found",
			req: &userspb.LoginUserRequest{
				Email:    "test@gmail.com",
				Password: "password12345",
			},
			mock: func(req *userspb.LoginUserRequest) {
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

func TestGetUser(t *testing.T) {
	ctrl := gomock.NewController(t)

	// Create a mock repository
	repo := usersmock.NewMockRepository(ctrl)

	// Create a new service
	s := users.New(validator.New(), repo)

	type want struct {
		*usersmodel.GetUserResponse
	}

	var (
		createdAt = time.Now()
		updatedAt = time.Now()
	)

	// Test cases
	tests := []struct {
		name  string
		req   *userspb.GetUserRequest
		mock  func(req *userspb.GetUserRequest)
		want  want
		isErr bool
	}{
		{
			name: "success",
			req: &userspb.GetUserRequest{
				Id: "1",
			},
			mock: func(req *userspb.GetUserRequest) {
				repo.EXPECT().GetUser(
					gomock.Any(),
					req.GetId(),
				).Return(&usersmodel.GetUserResponse{
					ID:                     "userID",
					Email:                  "user@example.com",
					NotificationPreference: "ALERTS",
					CreatedAt:              createdAt,
					UpdatedAt:              updatedAt,
				}, nil)
			},
			want: want{
				GetUserResponse: &usersmodel.GetUserResponse{
					ID:                     "userID",
					Email:                  "user@example.com",
					NotificationPreference: "ALERTS",
					CreatedAt:              createdAt,
					UpdatedAt:              updatedAt,
				},
			},
			isErr: false,
		},
		{
			name: "error: missing required fields in request",
			req: &userspb.GetUserRequest{
				Id: "",
			},
			mock:  func(_ *userspb.GetUserRequest) {},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: invalid user",
			req: &userspb.GetUserRequest{
				Id: "invalid_user_id",
			},
			mock: func(req *userspb.GetUserRequest) {
				repo.EXPECT().GetUser(
					gomock.Any(),
					req.GetId(),
				).Return(nil, status.Error(codes.NotFound, "invalid user"))
			},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: user not found",
			req: &userspb.GetUserRequest{
				Id: "invalid_user_id",
			},
			mock: func(req *userspb.GetUserRequest) {
				repo.EXPECT().GetUser(
					gomock.Any(),
					req.GetId(),
				).Return(nil, status.Error(codes.NotFound, "user not found"))
			},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: internal server error",
			req: &userspb.GetUserRequest{
				Id: "user_id",
			},
			mock: func(req *userspb.GetUserRequest) {
				repo.EXPECT().GetUser(
					gomock.Any(),
					req.GetId(),
				).Return(nil, status.Error(codes.Internal, "internal server error"))
			},
			want:  want{},
			isErr: true,
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		tt.mock(tt.req)
		t.Run(tt.name, func(t *testing.T) {
			user, err := s.GetUser(t.Context(), tt.req)
			if tt.isErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			assert.Equal(t, tt.want.GetUserResponse, user)
		})
	}
}

func TestUpdateUser(t *testing.T) {
	ctrl := gomock.NewController(t)

	// Create a mock repository
	repo := usersmock.NewMockRepository(ctrl)

	// Create a new service
	s := users.New(validator.New(), repo)

	tests := []struct {
		name  string
		req   *userspb.UpdateUserRequest
		mock  func(req *userspb.UpdateUserRequest)
		isErr bool
	}{
		{
			name: "success",
			req: &userspb.UpdateUserRequest{
				Id:                     "1",
				Password:               "newpassword",
				NotificationPreference: "ALERTS",
			},
			mock: func(req *userspb.UpdateUserRequest) {
				repo.EXPECT().UpdateUser(
					gomock.Any(),
					req.GetId(),
					req.GetPassword(),
					req.GetNotificationPreference(),
				).Return(nil)
			},
			isErr: false,
		},
		{
			name: "error: missing required fields in request",
			req: &userspb.UpdateUserRequest{
				Id:                     "",
				Password:               "",
				NotificationPreference: "",
			},
			mock:  func(_ *userspb.UpdateUserRequest) {},
			isErr: true,
		},
		{
			name: "error: invalid user",
			req: &userspb.UpdateUserRequest{
				Id:                     "invalid_user_id",
				Password:               "newpassword",
				NotificationPreference: "ALERTS",
			},
			mock: func(req *userspb.UpdateUserRequest) {
				repo.EXPECT().UpdateUser(
					gomock.Any(),
					req.GetId(),
					req.GetPassword(),
					req.GetNotificationPreference(),
				).Return(status.Error(codes.NotFound, "invalid user"))
			},
			isErr: true,
		},
		{
			name: "error: user not found",
			req: &userspb.UpdateUserRequest{
				Id:                     "invalid_user_id",
				Password:               "newpassword",
				NotificationPreference: "ALERTS",
			},
			mock: func(req *userspb.UpdateUserRequest) {
				repo.EXPECT().UpdateUser(
					gomock.Any(),
					req.GetId(),
					req.GetPassword(),
					req.GetNotificationPreference(),
				).Return(status.Error(codes.NotFound, "user not found"))
			},
			isErr: true,
		},
		{
			name: "error: invalid notification preference",
			req: &userspb.UpdateUserRequest{
				Id:                     "user_id",
				Password:               "newpassword",
				NotificationPreference: "INVALID_PREFERENCE",
			},
			mock: func(req *userspb.UpdateUserRequest) {
				repo.EXPECT().UpdateUser(
					gomock.Any(),
					req.GetId(),
					req.GetPassword(),
					req.GetNotificationPreference(),
				).Return(status.Error(codes.InvalidArgument, "invalid notification preference"))
			},
			isErr: true,
		},
		{
			name: "error: internal server error",
			req: &userspb.UpdateUserRequest{
				Id:                     "user_id",
				Password:               "newpassword",
				NotificationPreference: "ALERTS",
			},
			mock: func(req *userspb.UpdateUserRequest) {
				repo.EXPECT().UpdateUser(
					gomock.Any(),
					req.GetId(),
					req.GetPassword(),
					req.GetNotificationPreference(),
				).Return(status.Error(codes.Internal, "internal server error"))
			},
			isErr: true,
		},
	}

	defer ctrl.Finish()

	for _, tt := range tests {
		tt.mock(tt.req)
		t.Run(tt.name, func(t *testing.T) {
			err := s.UpdateUser(t.Context(), tt.req)

			if (err != nil) != tt.isErr {
				t.Errorf("UpdateUser() error = %v, wantErr %v", err, tt.isErr)
				return
			}
		})
	}
}
