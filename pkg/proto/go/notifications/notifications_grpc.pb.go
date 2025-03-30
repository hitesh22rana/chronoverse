// Code generated by protoc-gen-go-grpc. DO NOT EDIT.
// versions:
// - protoc-gen-go-grpc v1.5.1
// - protoc             (unknown)
// source: notifications/notifications.proto

package notifications

import (
	context "context"
	grpc "google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
)

// This is a compile-time assertion to ensure that this generated file
// is compatible with the grpc package it is being compiled against.
// Requires gRPC-Go v1.64.0 or later.
const _ = grpc.SupportPackageIsVersion9

const (
	NotificationsService_CreateNotification_FullMethodName    = "/notifications.NotificationsService/CreateNotification"
	NotificationsService_MarkNotificationsRead_FullMethodName = "/notifications.NotificationsService/MarkNotificationsRead"
	NotificationsService_ListNotifications_FullMethodName     = "/notifications.NotificationsService/ListNotifications"
)

// NotificationsServiceClient is the client API for NotificationsService service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
//
// NotificationsService contains the service definition for the Notifications service.
type NotificationsServiceClient interface {
	// CreateNotification creates a new notification.
	// This is an internal API and should not be exposed to the public.
	CreateNotification(ctx context.Context, in *CreateNotificationRequest, opts ...grpc.CallOption) (*CreateNotificationResponse, error)
	// MarkNotificationsRead marks a list of notifications as read.
	MarkNotificationsRead(ctx context.Context, in *MarkNotificationsReadRequest, opts ...grpc.CallOption) (*MarkNotificationsReadResponse, error)
	// ListNotifications returns a list of notifications.
	ListNotifications(ctx context.Context, in *ListNotificationsRequest, opts ...grpc.CallOption) (*ListNotificationsResponse, error)
}

type notificationsServiceClient struct {
	cc grpc.ClientConnInterface
}

func NewNotificationsServiceClient(cc grpc.ClientConnInterface) NotificationsServiceClient {
	return &notificationsServiceClient{cc}
}

func (c *notificationsServiceClient) CreateNotification(ctx context.Context, in *CreateNotificationRequest, opts ...grpc.CallOption) (*CreateNotificationResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(CreateNotificationResponse)
	err := c.cc.Invoke(ctx, NotificationsService_CreateNotification_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *notificationsServiceClient) MarkNotificationsRead(ctx context.Context, in *MarkNotificationsReadRequest, opts ...grpc.CallOption) (*MarkNotificationsReadResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(MarkNotificationsReadResponse)
	err := c.cc.Invoke(ctx, NotificationsService_MarkNotificationsRead_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *notificationsServiceClient) ListNotifications(ctx context.Context, in *ListNotificationsRequest, opts ...grpc.CallOption) (*ListNotificationsResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(ListNotificationsResponse)
	err := c.cc.Invoke(ctx, NotificationsService_ListNotifications_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// NotificationsServiceServer is the server API for NotificationsService service.
// All implementations should embed UnimplementedNotificationsServiceServer
// for forward compatibility.
//
// NotificationsService contains the service definition for the Notifications service.
type NotificationsServiceServer interface {
	// CreateNotification creates a new notification.
	// This is an internal API and should not be exposed to the public.
	CreateNotification(context.Context, *CreateNotificationRequest) (*CreateNotificationResponse, error)
	// MarkNotificationsRead marks a list of notifications as read.
	MarkNotificationsRead(context.Context, *MarkNotificationsReadRequest) (*MarkNotificationsReadResponse, error)
	// ListNotifications returns a list of notifications.
	ListNotifications(context.Context, *ListNotificationsRequest) (*ListNotificationsResponse, error)
}

// UnimplementedNotificationsServiceServer should be embedded to have
// forward compatible implementations.
//
// NOTE: this should be embedded by value instead of pointer to avoid a nil
// pointer dereference when methods are called.
type UnimplementedNotificationsServiceServer struct{}

func (UnimplementedNotificationsServiceServer) CreateNotification(context.Context, *CreateNotificationRequest) (*CreateNotificationResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method CreateNotification not implemented")
}
func (UnimplementedNotificationsServiceServer) MarkNotificationsRead(context.Context, *MarkNotificationsReadRequest) (*MarkNotificationsReadResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method MarkNotificationsRead not implemented")
}
func (UnimplementedNotificationsServiceServer) ListNotifications(context.Context, *ListNotificationsRequest) (*ListNotificationsResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method ListNotifications not implemented")
}
func (UnimplementedNotificationsServiceServer) testEmbeddedByValue() {}

// UnsafeNotificationsServiceServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to NotificationsServiceServer will
// result in compilation errors.
type UnsafeNotificationsServiceServer interface {
	mustEmbedUnimplementedNotificationsServiceServer()
}

func RegisterNotificationsServiceServer(s grpc.ServiceRegistrar, srv NotificationsServiceServer) {
	// If the following call pancis, it indicates UnimplementedNotificationsServiceServer was
	// embedded by pointer and is nil.  This will cause panics if an
	// unimplemented method is ever invoked, so we test this at initialization
	// time to prevent it from happening at runtime later due to I/O.
	if t, ok := srv.(interface{ testEmbeddedByValue() }); ok {
		t.testEmbeddedByValue()
	}
	s.RegisterService(&NotificationsService_ServiceDesc, srv)
}

func _NotificationsService_CreateNotification_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(CreateNotificationRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(NotificationsServiceServer).CreateNotification(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: NotificationsService_CreateNotification_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(NotificationsServiceServer).CreateNotification(ctx, req.(*CreateNotificationRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _NotificationsService_MarkNotificationsRead_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(MarkNotificationsReadRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(NotificationsServiceServer).MarkNotificationsRead(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: NotificationsService_MarkNotificationsRead_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(NotificationsServiceServer).MarkNotificationsRead(ctx, req.(*MarkNotificationsReadRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _NotificationsService_ListNotifications_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ListNotificationsRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(NotificationsServiceServer).ListNotifications(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: NotificationsService_ListNotifications_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(NotificationsServiceServer).ListNotifications(ctx, req.(*ListNotificationsRequest))
	}
	return interceptor(ctx, in, info, handler)
}

// NotificationsService_ServiceDesc is the grpc.ServiceDesc for NotificationsService service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var NotificationsService_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "notifications.NotificationsService",
	HandlerType: (*NotificationsServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "CreateNotification",
			Handler:    _NotificationsService_CreateNotification_Handler,
		},
		{
			MethodName: "MarkNotificationsRead",
			Handler:    _NotificationsService_MarkNotificationsRead_Handler,
		},
		{
			MethodName: "ListNotifications",
			Handler:    _NotificationsService_ListNotifications_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "notifications/notifications.proto",
}
