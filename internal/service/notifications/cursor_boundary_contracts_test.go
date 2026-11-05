package notifications_test

import (
	"context"
	"encoding/base64"
	"reflect"
	"testing"
	"time"

	"github.com/go-playground/validator/v10"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	model "github.com/hitesh22rana/chronoverse/internal/model/notifications"
	service "github.com/hitesh22rana/chronoverse/internal/service/notifications"
	mocks "github.com/hitesh22rana/chronoverse/internal/service/notifications/mock"
	notificationspb "github.com/hitesh22rana/chronoverse/pkg/proto/go/notifications"
)

const (
	cursorBoundaryOwner = "00000000-0000-4000-8000-000000000011"
	cursorBoundaryItem  = "00000000-0000-4000-8000-000000000012"
)

type cursorBoundaryKey struct{}

func TestListNotificationsDecodesCursorAndPreservesCaller(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := mocks.NewMockRepository(ctrl)
	raw := cursorBoundaryItem + "$2026-11-05T01:02:03.123456Z"
	req := &notificationspb.ListNotificationsRequest{UserId: cursorBoundaryOwner, Cursor: base64.StdEncoding.EncodeToString([]byte(raw))}
	original := proto.Clone(req)

	svc := service.New(validator.New(), repo)
	ctx, cancel := context.WithTimeout(context.WithValue(t.Context(), cursorBoundaryKey{}, "caller"), 5*time.Second)
	defer cancel()
	repo.EXPECT().ListNotifications(gomock.Any(), cursorBoundaryOwner, raw).DoAndReturn(func(got context.Context, _, _ string) (*model.ListNotificationsResponse, error) {
		if got.Value(cursorBoundaryKey{}) != "caller" || got.Done() != ctx.Done() {
			t.Error("repository lost caller context")
		}
		return &model.ListNotificationsResponse{}, nil
	})
	got, err := svc.ListNotifications(ctx, req)
	if err != nil || got == nil || !reflect.DeepEqual(*got, model.ListNotificationsResponse{}) {
		t.Fatalf("response=%+v err=%v", got, err)
	}
	if !proto.Equal(req, original) {
		t.Fatal("request was mutated")
	}
}

func TestListNotificationsRejectsBadInputsBeforeRepository(t *testing.T) {
	for _, name := range []string{"cursor", "owner"} {
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := mocks.NewMockRepository(ctrl)
			svc := service.New(validator.New(), repo)
			req := &notificationspb.ListNotificationsRequest{UserId: cursorBoundaryOwner}
			if name == "cursor" {
				req.Cursor = "%%%"
			} else {
				req.UserId = ""
			}
			before := proto.Clone(req)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			got, err := svc.ListNotifications(ctx, req)
			if got != nil || status.Code(err) != codes.InvalidArgument {
				t.Fatalf("response=%+v err=%v, want InvalidArgument", got, err)
			}
			if !proto.Equal(req, before) {
				t.Fatal("invalid request was mutated")
			}
		})
	}
}
