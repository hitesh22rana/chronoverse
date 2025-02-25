// Code generated by protoc-gen-go-grpc. DO NOT EDIT.
// versions:
// - protoc-gen-go-grpc v1.5.1
// - protoc             (unknown)
// source: jobs/jobs.proto

package jobs

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
	JobsService_CreateJob_FullMethodName         = "/jobs.JobsService/CreateJob"
	JobsService_UpdateJob_FullMethodName         = "/jobs.JobsService/UpdateJob"
	JobsService_GetJob_FullMethodName            = "/jobs.JobsService/GetJob"
	JobsService_GetJobByID_FullMethodName        = "/jobs.JobsService/GetJobByID"
	JobsService_ListJobsByUserID_FullMethodName  = "/jobs.JobsService/ListJobsByUserID"
	JobsService_ListScheduledJobs_FullMethodName = "/jobs.JobsService/ListScheduledJobs"
)

// JobsServiceClient is the client API for JobsService service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
//
// JobsService handles job related operations.
type JobsServiceClient interface {
	// CreateJob a new job.
	CreateJob(ctx context.Context, in *CreateJobRequest, opts ...grpc.CallOption) (*CreateJobResponse, error)
	// UpdateJob an existing job.
	UpdateJob(ctx context.Context, in *UpdateJobRequest, opts ...grpc.CallOption) (*UpdateJobResponse, error)
	// GetJob a job by ID and user_id.
	GetJob(ctx context.Context, in *GetJobRequest, opts ...grpc.CallOption) (*GetJobResponse, error)
	// GetJobByID a job by ID.
	// This is an internal API and should not be exposed to the public.
	GetJobByID(ctx context.Context, in *GetJobByIDRequest, opts ...grpc.CallOption) (*GetJobByIDResponse, error)
	// ListJobsByUserID returns a list of all jobs for a user_id.
	ListJobsByUserID(ctx context.Context, in *ListJobsByUserIDRequest, opts ...grpc.CallOption) (*ListJobsByUserIDResponse, error)
	// ListScheduledJobs returns a list of all scheduled jobs for a job_id.
	ListScheduledJobs(ctx context.Context, in *ListScheduledJobsRequest, opts ...grpc.CallOption) (*ListScheduledJobsResponse, error)
}

type jobsServiceClient struct {
	cc grpc.ClientConnInterface
}

func NewJobsServiceClient(cc grpc.ClientConnInterface) JobsServiceClient {
	return &jobsServiceClient{cc}
}

func (c *jobsServiceClient) CreateJob(ctx context.Context, in *CreateJobRequest, opts ...grpc.CallOption) (*CreateJobResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(CreateJobResponse)
	err := c.cc.Invoke(ctx, JobsService_CreateJob_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *jobsServiceClient) UpdateJob(ctx context.Context, in *UpdateJobRequest, opts ...grpc.CallOption) (*UpdateJobResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(UpdateJobResponse)
	err := c.cc.Invoke(ctx, JobsService_UpdateJob_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *jobsServiceClient) GetJob(ctx context.Context, in *GetJobRequest, opts ...grpc.CallOption) (*GetJobResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(GetJobResponse)
	err := c.cc.Invoke(ctx, JobsService_GetJob_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *jobsServiceClient) GetJobByID(ctx context.Context, in *GetJobByIDRequest, opts ...grpc.CallOption) (*GetJobByIDResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(GetJobByIDResponse)
	err := c.cc.Invoke(ctx, JobsService_GetJobByID_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *jobsServiceClient) ListJobsByUserID(ctx context.Context, in *ListJobsByUserIDRequest, opts ...grpc.CallOption) (*ListJobsByUserIDResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(ListJobsByUserIDResponse)
	err := c.cc.Invoke(ctx, JobsService_ListJobsByUserID_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *jobsServiceClient) ListScheduledJobs(ctx context.Context, in *ListScheduledJobsRequest, opts ...grpc.CallOption) (*ListScheduledJobsResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(ListScheduledJobsResponse)
	err := c.cc.Invoke(ctx, JobsService_ListScheduledJobs_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// JobsServiceServer is the server API for JobsService service.
// All implementations should embed UnimplementedJobsServiceServer
// for forward compatibility.
//
// JobsService handles job related operations.
type JobsServiceServer interface {
	// CreateJob a new job.
	CreateJob(context.Context, *CreateJobRequest) (*CreateJobResponse, error)
	// UpdateJob an existing job.
	UpdateJob(context.Context, *UpdateJobRequest) (*UpdateJobResponse, error)
	// GetJob a job by ID and user_id.
	GetJob(context.Context, *GetJobRequest) (*GetJobResponse, error)
	// GetJobByID a job by ID.
	// This is an internal API and should not be exposed to the public.
	GetJobByID(context.Context, *GetJobByIDRequest) (*GetJobByIDResponse, error)
	// ListJobsByUserID returns a list of all jobs for a user_id.
	ListJobsByUserID(context.Context, *ListJobsByUserIDRequest) (*ListJobsByUserIDResponse, error)
	// ListScheduledJobs returns a list of all scheduled jobs for a job_id.
	ListScheduledJobs(context.Context, *ListScheduledJobsRequest) (*ListScheduledJobsResponse, error)
}

// UnimplementedJobsServiceServer should be embedded to have
// forward compatible implementations.
//
// NOTE: this should be embedded by value instead of pointer to avoid a nil
// pointer dereference when methods are called.
type UnimplementedJobsServiceServer struct{}

func (UnimplementedJobsServiceServer) CreateJob(context.Context, *CreateJobRequest) (*CreateJobResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method CreateJob not implemented")
}
func (UnimplementedJobsServiceServer) UpdateJob(context.Context, *UpdateJobRequest) (*UpdateJobResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method UpdateJob not implemented")
}
func (UnimplementedJobsServiceServer) GetJob(context.Context, *GetJobRequest) (*GetJobResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method GetJob not implemented")
}
func (UnimplementedJobsServiceServer) GetJobByID(context.Context, *GetJobByIDRequest) (*GetJobByIDResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method GetJobByID not implemented")
}
func (UnimplementedJobsServiceServer) ListJobsByUserID(context.Context, *ListJobsByUserIDRequest) (*ListJobsByUserIDResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method ListJobsByUserID not implemented")
}
func (UnimplementedJobsServiceServer) ListScheduledJobs(context.Context, *ListScheduledJobsRequest) (*ListScheduledJobsResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method ListScheduledJobs not implemented")
}
func (UnimplementedJobsServiceServer) testEmbeddedByValue() {}

// UnsafeJobsServiceServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to JobsServiceServer will
// result in compilation errors.
type UnsafeJobsServiceServer interface {
	mustEmbedUnimplementedJobsServiceServer()
}

func RegisterJobsServiceServer(s grpc.ServiceRegistrar, srv JobsServiceServer) {
	// If the following call pancis, it indicates UnimplementedJobsServiceServer was
	// embedded by pointer and is nil.  This will cause panics if an
	// unimplemented method is ever invoked, so we test this at initialization
	// time to prevent it from happening at runtime later due to I/O.
	if t, ok := srv.(interface{ testEmbeddedByValue() }); ok {
		t.testEmbeddedByValue()
	}
	s.RegisterService(&JobsService_ServiceDesc, srv)
}

func _JobsService_CreateJob_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(CreateJobRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(JobsServiceServer).CreateJob(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: JobsService_CreateJob_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(JobsServiceServer).CreateJob(ctx, req.(*CreateJobRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _JobsService_UpdateJob_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(UpdateJobRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(JobsServiceServer).UpdateJob(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: JobsService_UpdateJob_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(JobsServiceServer).UpdateJob(ctx, req.(*UpdateJobRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _JobsService_GetJob_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(GetJobRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(JobsServiceServer).GetJob(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: JobsService_GetJob_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(JobsServiceServer).GetJob(ctx, req.(*GetJobRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _JobsService_GetJobByID_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(GetJobByIDRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(JobsServiceServer).GetJobByID(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: JobsService_GetJobByID_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(JobsServiceServer).GetJobByID(ctx, req.(*GetJobByIDRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _JobsService_ListJobsByUserID_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ListJobsByUserIDRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(JobsServiceServer).ListJobsByUserID(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: JobsService_ListJobsByUserID_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(JobsServiceServer).ListJobsByUserID(ctx, req.(*ListJobsByUserIDRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _JobsService_ListScheduledJobs_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ListScheduledJobsRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(JobsServiceServer).ListScheduledJobs(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: JobsService_ListScheduledJobs_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(JobsServiceServer).ListScheduledJobs(ctx, req.(*ListScheduledJobsRequest))
	}
	return interceptor(ctx, in, info, handler)
}

// JobsService_ServiceDesc is the grpc.ServiceDesc for JobsService service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var JobsService_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "jobs.JobsService",
	HandlerType: (*JobsServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "CreateJob",
			Handler:    _JobsService_CreateJob_Handler,
		},
		{
			MethodName: "UpdateJob",
			Handler:    _JobsService_UpdateJob_Handler,
		},
		{
			MethodName: "GetJob",
			Handler:    _JobsService_GetJob_Handler,
		},
		{
			MethodName: "GetJobByID",
			Handler:    _JobsService_GetJobByID_Handler,
		},
		{
			MethodName: "ListJobsByUserID",
			Handler:    _JobsService_ListJobsByUserID_Handler,
		},
		{
			MethodName: "ListScheduledJobs",
			Handler:    _JobsService_ListScheduledJobs_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "jobs/jobs.proto",
}
