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

	repo := usersmock.NewMockRepository(ctrl)

	s := users.New(validator.New(), repo)

	type want struct {
		userID string
		pat    string
	}

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
				Email:          "test@gmail.com",
				Password:       "BlueFalcon$22Dances!",
				IdempotencyKey: "register-key",
			},
			mock: func(req *userspb.RegisterUserRequest) {
				repo.EXPECT().RegisterUser(
					gomock.Any(),
					req.GetEmail(),
					req.GetPassword(),
					req.GetIdempotencyKey(),
				).Return(&usersmodel.GetUserResponse{
					ID:                     "userID",
					Email:                  "user@example.com",
					NotificationPreference: "ALERTS",
					CreatedAt:              time.Now(),
					UpdatedAt:              time.Now(),
				}, "token", nil)
			},
			want: want{
				userID: "userID",
				pat:    "token",
			},
			isErr: false,
		},
		{
			name: "error: invalid email",
			req: &userspb.RegisterUserRequest{
				Email:          "test",
				Password:       "password12345",
				IdempotencyKey: "register-key",
			},
			mock:  func(_ *userspb.RegisterUserRequest) {},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: password too short",
			req: &userspb.RegisterUserRequest{
				Email:          "test@gmail.com",
				Password:       "pass",
				IdempotencyKey: "register-key",
			},
			mock:  func(_ *userspb.RegisterUserRequest) {},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: password too long",
			req: &userspb.RegisterUserRequest{
				Email:          "test@gmail.com",
				Password:       "password1234567890123456789012345678901234567890123456789012345678901234567890",
				IdempotencyKey: "register-key",
			},
			mock:  func(_ *userspb.RegisterUserRequest) {},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: weak predictable password scores below 3",
			req: &userspb.RegisterUserRequest{
				Email:          "test@gmail.com",
				Password:       "Password123",
				IdempotencyKey: "register-key",
			},
			mock:  func(_ *userspb.RegisterUserRequest) {},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: weak password with letters and digits only",
			req: &userspb.RegisterUserRequest{
				Email:          "test@gmail.com",
				Password:       "password12345",
				IdempotencyKey: "register-key",
			},
			mock:  func(_ *userspb.RegisterUserRequest) {},
			want:  want{},
			isErr: true,
		},
		{
			name: "error: weak sequential password",
			req: &userspb.RegisterUserRequest{
				Email:          "test@gmail.com",
				Password:       "abcdefghij",
				IdempotencyKey: "register-key",
			},
			mock:  func(_ *userspb.RegisterUserRequest) {},
			want:  want{},
			isErr: true,
		},
		{
			name: "success: email case and whitespace normalized",
			req: &userspb.RegisterUserRequest{
				Email:          "  Test@Gmail.COM  ",
				Password:       "BlueFalcon$22Dances!",
				IdempotencyKey: "register-key",
			},
			mock: func(_ *userspb.RegisterUserRequest) {
				repo.EXPECT().RegisterUser(
					gomock.Any(),
					"test@gmail.com",
					"BlueFalcon$22Dances!",
					"register-key",
				).Return(&usersmodel.GetUserResponse{
					ID:                     "userID",
					Email:                  "test@gmail.com",
					NotificationPreference: "ALERTS",
					CreatedAt:              time.Now(),
					UpdatedAt:              time.Now(),
				}, "token", nil)
			},
			want: want{
				userID: "userID",
				pat:    "token",
			},
			isErr: false,
		},
		{
			name: "error: already exists",
			req: &userspb.RegisterUserRequest{
				Email:          "test@gmail.com",
				Password:       "BlueFalcon$22Dances!",
				IdempotencyKey: "register-key",
			},
			mock: func(req *userspb.RegisterUserRequest) {
				repo.EXPECT().RegisterUser(
					gomock.Any(),
					req.GetEmail(),
					req.GetPassword(),
					req.GetIdempotencyKey(),
				).Return(nil, "", status.Errorf(codes.AlreadyExists, "user already exists"))
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

	repo := usersmock.NewMockRepository(ctrl)

	s := users.New(validator.New(), repo)

	type want struct {
		userID string
		pat    string
		code   codes.Code
	}

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
				).Return(&usersmodel.GetUserResponse{
					ID:                     "userID",
					Email:                  "user@example.com",
					NotificationPreference: "ALERTS",
					CreatedAt:              time.Now(),
					UpdatedAt:              time.Now(),
				}, "token", nil)
			},
			want: want{
				userID: "userID",
				pat:    "token",
				code:   codes.OK,
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
			want:  want{code: codes.InvalidArgument},
			isErr: true,
		},
		{
			name: "error: password too short",
			req: &userspb.LoginUserRequest{
				Email:    "test@gmail.com",
				Password: "pass",
			},
			mock:  func(_ *userspb.LoginUserRequest) {},
			want:  want{code: codes.InvalidArgument},
			isErr: true,
		},
		{
			name: "error: password too long",
			req: &userspb.LoginUserRequest{
				Email:    "test@gmail.com",
				Password: "password1234567890123456789012345678901234567890123456789012345678901234567890",
			},
			mock:  func(_ *userspb.LoginUserRequest) {},
			want:  want{code: codes.InvalidArgument},
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
				).Return(nil, "", status.Errorf(codes.NotFound, "user not found"))
			},
			want:  want{code: codes.Unauthenticated},
			isErr: true,
		},
		{
			name: "error: invalid password",
			req: &userspb.LoginUserRequest{
				Email:    "test@gmail.com",
				Password: "password12345",
			},
			mock: func(req *userspb.LoginUserRequest) {
				repo.EXPECT().LoginUser(
					gomock.Any(),
					req.GetEmail(),
					req.GetPassword(),
				).Return(nil, "", status.Errorf(codes.InvalidArgument, "invalid password"))
			},
			want:  want{code: codes.Unauthenticated},
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
			if got := status.Code(err); got != tt.want.code {
				t.Errorf("LoginUser() code = %v, want %v", got, tt.want.code)
			}
		})
	}
}

func TestGetUser(t *testing.T) {
	ctrl := gomock.NewController(t)

	repo := usersmock.NewMockRepository(ctrl)

	s := users.New(validator.New(), repo)

	type want struct {
		*usersmodel.GetUserResponse
	}

	var (
		createdAt = time.Now()
		updatedAt = time.Now()
	)

	tests := []struct {
		name  string
		req   *userspb.GetUserRequest
		mock  func(req *userspb.GetUserRequest)
		want  want
		isErr bool
	}{
		{
			name: "success: repository result",
			req: &userspb.GetUserRequest{
				Id: "userID",
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

	repo := usersmock.NewMockRepository(ctrl)

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
				Id:                     "userID",
				NotificationPreference: "ALERTS",
			},
			mock: func(req *userspb.UpdateUserRequest) {
				repo.EXPECT().UpdateUser(
					gomock.Any(),
					req.GetId(),
					req.GetNotificationPreference(),
				).Return(nil)
			},
			isErr: false,
		},
		{
			name: "error: missing required fields in request",
			req: &userspb.UpdateUserRequest{
				Id:                     "",
				NotificationPreference: "",
			},
			mock:  func(_ *userspb.UpdateUserRequest) {},
			isErr: true,
		},
		{
			name: "error: invalid user",
			req: &userspb.UpdateUserRequest{
				Id:                     "invalid_user_id",
				NotificationPreference: "ALERTS",
			},
			mock: func(req *userspb.UpdateUserRequest) {
				repo.EXPECT().UpdateUser(
					gomock.Any(),
					req.GetId(),
					req.GetNotificationPreference(),
				).Return(status.Error(codes.NotFound, "invalid user"))
			},
			isErr: true,
		},
		{
			name: "error: user not found",
			req: &userspb.UpdateUserRequest{
				Id:                     "invalid_user_id",
				NotificationPreference: "ALERTS",
			},
			mock: func(req *userspb.UpdateUserRequest) {
				repo.EXPECT().UpdateUser(
					gomock.Any(),
					req.GetId(),
					req.GetNotificationPreference(),
				).Return(status.Error(codes.NotFound, "user not found"))
			},
			isErr: true,
		},
		{
			name: "error: invalid notification preference",
			req: &userspb.UpdateUserRequest{
				Id:                     "user_id",
				NotificationPreference: "INVALID_PREFERENCE",
			},
			mock:  func(_ *userspb.UpdateUserRequest) {},
			isErr: true,
		},
		{
			name: "error: internal server error",
			req: &userspb.UpdateUserRequest{
				Id:                     "user_id",
				NotificationPreference: "ALERTS",
			},
			mock: func(req *userspb.UpdateUserRequest) {
				repo.EXPECT().UpdateUser(
					gomock.Any(),
					req.GetId(),
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
