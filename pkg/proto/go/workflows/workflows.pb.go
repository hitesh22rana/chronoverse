// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.36.6
// 	protoc        (unknown)
// source: workflows/workflows.proto

package workflows

import (
	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	protoimpl "google.golang.org/protobuf/runtime/protoimpl"
	reflect "reflect"
	sync "sync"
	unsafe "unsafe"
)

const (
	// Verify that this generated code is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(20 - protoimpl.MinVersion)
	// Verify that runtime/protoimpl is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(protoimpl.MaxVersion - 20)
)

// CreateWorkflowRequest contains the details needed to create a new workflow.
type CreateWorkflowRequest struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	UserId        string                 `protobuf:"bytes,1,opt,name=user_id,json=userId,proto3" json:"user_id,omitempty"` // ID of the user
	Name          string                 `protobuf:"bytes,2,opt,name=name,proto3" json:"name,omitempty"`                   // Name of the workflow
	Payload       string                 `protobuf:"bytes,3,opt,name=payload,proto3" json:"payload,omitempty"`             // JSON string for payload
	Kind          string                 `protobuf:"bytes,4,opt,name=kind,proto3" json:"kind,omitempty"`                   // Kind of workflow
	Interval      int32                  `protobuf:"varint,5,opt,name=interval,proto3" json:"interval,omitempty"`          // Interval measured in minutes
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *CreateWorkflowRequest) Reset() {
	*x = CreateWorkflowRequest{}
	mi := &file_workflows_workflows_proto_msgTypes[0]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *CreateWorkflowRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*CreateWorkflowRequest) ProtoMessage() {}

func (x *CreateWorkflowRequest) ProtoReflect() protoreflect.Message {
	mi := &file_workflows_workflows_proto_msgTypes[0]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use CreateWorkflowRequest.ProtoReflect.Descriptor instead.
func (*CreateWorkflowRequest) Descriptor() ([]byte, []int) {
	return file_workflows_workflows_proto_rawDescGZIP(), []int{0}
}

func (x *CreateWorkflowRequest) GetUserId() string {
	if x != nil {
		return x.UserId
	}
	return ""
}

func (x *CreateWorkflowRequest) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

func (x *CreateWorkflowRequest) GetPayload() string {
	if x != nil {
		return x.Payload
	}
	return ""
}

func (x *CreateWorkflowRequest) GetKind() string {
	if x != nil {
		return x.Kind
	}
	return ""
}

func (x *CreateWorkflowRequest) GetInterval() int32 {
	if x != nil {
		return x.Interval
	}
	return 0
}

// CreateWorkflowResponse contains the result of a workflow creation attempt.
type CreateWorkflowResponse struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	Id            string                 `protobuf:"bytes,1,opt,name=id,proto3" json:"id,omitempty"` // ID of the workflow
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *CreateWorkflowResponse) Reset() {
	*x = CreateWorkflowResponse{}
	mi := &file_workflows_workflows_proto_msgTypes[1]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *CreateWorkflowResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*CreateWorkflowResponse) ProtoMessage() {}

func (x *CreateWorkflowResponse) ProtoReflect() protoreflect.Message {
	mi := &file_workflows_workflows_proto_msgTypes[1]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use CreateWorkflowResponse.ProtoReflect.Descriptor instead.
func (*CreateWorkflowResponse) Descriptor() ([]byte, []int) {
	return file_workflows_workflows_proto_rawDescGZIP(), []int{1}
}

func (x *CreateWorkflowResponse) GetId() string {
	if x != nil {
		return x.Id
	}
	return ""
}

// UpdateWorkflowRequest contains the details needed to update a workflow.
type UpdateWorkflowRequest struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	Id            string                 `protobuf:"bytes,1,opt,name=id,proto3" json:"id,omitempty"`                       // ID of the workflow
	UserId        string                 `protobuf:"bytes,2,opt,name=user_id,json=userId,proto3" json:"user_id,omitempty"` // ID of the user
	Name          string                 `protobuf:"bytes,3,opt,name=name,proto3" json:"name,omitempty"`                   // Name of the workflow
	Payload       string                 `protobuf:"bytes,4,opt,name=payload,proto3" json:"payload,omitempty"`             // JSON string for payload
	Interval      int32                  `protobuf:"varint,5,opt,name=interval,proto3" json:"interval,omitempty"`          // Interval measured in minutes
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *UpdateWorkflowRequest) Reset() {
	*x = UpdateWorkflowRequest{}
	mi := &file_workflows_workflows_proto_msgTypes[2]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *UpdateWorkflowRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*UpdateWorkflowRequest) ProtoMessage() {}

func (x *UpdateWorkflowRequest) ProtoReflect() protoreflect.Message {
	mi := &file_workflows_workflows_proto_msgTypes[2]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use UpdateWorkflowRequest.ProtoReflect.Descriptor instead.
func (*UpdateWorkflowRequest) Descriptor() ([]byte, []int) {
	return file_workflows_workflows_proto_rawDescGZIP(), []int{2}
}

func (x *UpdateWorkflowRequest) GetId() string {
	if x != nil {
		return x.Id
	}
	return ""
}

func (x *UpdateWorkflowRequest) GetUserId() string {
	if x != nil {
		return x.UserId
	}
	return ""
}

func (x *UpdateWorkflowRequest) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

func (x *UpdateWorkflowRequest) GetPayload() string {
	if x != nil {
		return x.Payload
	}
	return ""
}

func (x *UpdateWorkflowRequest) GetInterval() int32 {
	if x != nil {
		return x.Interval
	}
	return 0
}

// UpdateWorkflowResponse contains the result of a workflow update attempt.
type UpdateWorkflowResponse struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *UpdateWorkflowResponse) Reset() {
	*x = UpdateWorkflowResponse{}
	mi := &file_workflows_workflows_proto_msgTypes[3]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *UpdateWorkflowResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*UpdateWorkflowResponse) ProtoMessage() {}

func (x *UpdateWorkflowResponse) ProtoReflect() protoreflect.Message {
	mi := &file_workflows_workflows_proto_msgTypes[3]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use UpdateWorkflowResponse.ProtoReflect.Descriptor instead.
func (*UpdateWorkflowResponse) Descriptor() ([]byte, []int) {
	return file_workflows_workflows_proto_rawDescGZIP(), []int{3}
}

// UpdateWorkflowBuildStatusRequest contains the details needed to update the build status of a workflow.
type UpdateWorkflowBuildStatusRequest struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	Id            string                 `protobuf:"bytes,1,opt,name=id,proto3" json:"id,omitempty"`                                      // ID of the workflow
	BuildStatus   string                 `protobuf:"bytes,2,opt,name=build_status,json=buildStatus,proto3" json:"build_status,omitempty"` // Status of the workflow
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *UpdateWorkflowBuildStatusRequest) Reset() {
	*x = UpdateWorkflowBuildStatusRequest{}
	mi := &file_workflows_workflows_proto_msgTypes[4]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *UpdateWorkflowBuildStatusRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*UpdateWorkflowBuildStatusRequest) ProtoMessage() {}

func (x *UpdateWorkflowBuildStatusRequest) ProtoReflect() protoreflect.Message {
	mi := &file_workflows_workflows_proto_msgTypes[4]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use UpdateWorkflowBuildStatusRequest.ProtoReflect.Descriptor instead.
func (*UpdateWorkflowBuildStatusRequest) Descriptor() ([]byte, []int) {
	return file_workflows_workflows_proto_rawDescGZIP(), []int{4}
}

func (x *UpdateWorkflowBuildStatusRequest) GetId() string {
	if x != nil {
		return x.Id
	}
	return ""
}

func (x *UpdateWorkflowBuildStatusRequest) GetBuildStatus() string {
	if x != nil {
		return x.BuildStatus
	}
	return ""
}

// UpdateWorkflowBuildStatusResponse contains the result of a workflow build status update attempt.
type UpdateWorkflowBuildStatusResponse struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *UpdateWorkflowBuildStatusResponse) Reset() {
	*x = UpdateWorkflowBuildStatusResponse{}
	mi := &file_workflows_workflows_proto_msgTypes[5]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *UpdateWorkflowBuildStatusResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*UpdateWorkflowBuildStatusResponse) ProtoMessage() {}

func (x *UpdateWorkflowBuildStatusResponse) ProtoReflect() protoreflect.Message {
	mi := &file_workflows_workflows_proto_msgTypes[5]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use UpdateWorkflowBuildStatusResponse.ProtoReflect.Descriptor instead.
func (*UpdateWorkflowBuildStatusResponse) Descriptor() ([]byte, []int) {
	return file_workflows_workflows_proto_rawDescGZIP(), []int{5}
}

// GetWorkflowRequest contains the details needed to get a workflow.
type GetWorkflowRequest struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	Id            string                 `protobuf:"bytes,1,opt,name=id,proto3" json:"id,omitempty"`                       // ID of the workflow
	UserId        string                 `protobuf:"bytes,2,opt,name=user_id,json=userId,proto3" json:"user_id,omitempty"` // ID of the user
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *GetWorkflowRequest) Reset() {
	*x = GetWorkflowRequest{}
	mi := &file_workflows_workflows_proto_msgTypes[6]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *GetWorkflowRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*GetWorkflowRequest) ProtoMessage() {}

func (x *GetWorkflowRequest) ProtoReflect() protoreflect.Message {
	mi := &file_workflows_workflows_proto_msgTypes[6]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use GetWorkflowRequest.ProtoReflect.Descriptor instead.
func (*GetWorkflowRequest) Descriptor() ([]byte, []int) {
	return file_workflows_workflows_proto_rawDescGZIP(), []int{6}
}

func (x *GetWorkflowRequest) GetId() string {
	if x != nil {
		return x.Id
	}
	return ""
}

func (x *GetWorkflowRequest) GetUserId() string {
	if x != nil {
		return x.UserId
	}
	return ""
}

// GetWorkflowResponse contains the result of a workflow retrieval attempt.
type GetWorkflowResponse struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	Id            string                 `protobuf:"bytes,1,opt,name=id,proto3" json:"id,omitempty"`                                         // ID of the workflow
	Name          string                 `protobuf:"bytes,2,opt,name=name,proto3" json:"name,omitempty"`                                     // Name of the workflow
	Payload       string                 `protobuf:"bytes,3,opt,name=payload,proto3" json:"payload,omitempty"`                               // JSON string for payload
	Kind          string                 `protobuf:"bytes,4,opt,name=kind,proto3" json:"kind,omitempty"`                                     // Kind of workflow
	BuildStatus   string                 `protobuf:"bytes,5,opt,name=build_status,json=buildStatus,proto3" json:"build_status,omitempty"`    // Status of the workflow
	Interval      int32                  `protobuf:"varint,6,opt,name=interval,proto3" json:"interval,omitempty"`                            // Interval measured in minutes
	CreatedAt     string                 `protobuf:"bytes,7,opt,name=created_at,json=createdAt,proto3" json:"created_at,omitempty"`          // Time the workflow was created
	UpdatedAt     string                 `protobuf:"bytes,8,opt,name=updated_at,json=updatedAt,proto3" json:"updated_at,omitempty"`          // Time the workflow was last updated
	TerminatedAt  string                 `protobuf:"bytes,9,opt,name=terminated_at,json=terminatedAt,proto3" json:"terminated_at,omitempty"` // Time the workflow was terminated
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *GetWorkflowResponse) Reset() {
	*x = GetWorkflowResponse{}
	mi := &file_workflows_workflows_proto_msgTypes[7]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *GetWorkflowResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*GetWorkflowResponse) ProtoMessage() {}

func (x *GetWorkflowResponse) ProtoReflect() protoreflect.Message {
	mi := &file_workflows_workflows_proto_msgTypes[7]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use GetWorkflowResponse.ProtoReflect.Descriptor instead.
func (*GetWorkflowResponse) Descriptor() ([]byte, []int) {
	return file_workflows_workflows_proto_rawDescGZIP(), []int{7}
}

func (x *GetWorkflowResponse) GetId() string {
	if x != nil {
		return x.Id
	}
	return ""
}

func (x *GetWorkflowResponse) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

func (x *GetWorkflowResponse) GetPayload() string {
	if x != nil {
		return x.Payload
	}
	return ""
}

func (x *GetWorkflowResponse) GetKind() string {
	if x != nil {
		return x.Kind
	}
	return ""
}

func (x *GetWorkflowResponse) GetBuildStatus() string {
	if x != nil {
		return x.BuildStatus
	}
	return ""
}

func (x *GetWorkflowResponse) GetInterval() int32 {
	if x != nil {
		return x.Interval
	}
	return 0
}

func (x *GetWorkflowResponse) GetCreatedAt() string {
	if x != nil {
		return x.CreatedAt
	}
	return ""
}

func (x *GetWorkflowResponse) GetUpdatedAt() string {
	if x != nil {
		return x.UpdatedAt
	}
	return ""
}

func (x *GetWorkflowResponse) GetTerminatedAt() string {
	if x != nil {
		return x.TerminatedAt
	}
	return ""
}

// GetWorkflowByIDRequest contains the details needed to get a workflow by workflow_id.
type GetWorkflowByIDRequest struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	Id            string                 `protobuf:"bytes,1,opt,name=id,proto3" json:"id,omitempty"` // ID of the workflow
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *GetWorkflowByIDRequest) Reset() {
	*x = GetWorkflowByIDRequest{}
	mi := &file_workflows_workflows_proto_msgTypes[8]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *GetWorkflowByIDRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*GetWorkflowByIDRequest) ProtoMessage() {}

func (x *GetWorkflowByIDRequest) ProtoReflect() protoreflect.Message {
	mi := &file_workflows_workflows_proto_msgTypes[8]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use GetWorkflowByIDRequest.ProtoReflect.Descriptor instead.
func (*GetWorkflowByIDRequest) Descriptor() ([]byte, []int) {
	return file_workflows_workflows_proto_rawDescGZIP(), []int{8}
}

func (x *GetWorkflowByIDRequest) GetId() string {
	if x != nil {
		return x.Id
	}
	return ""
}

// GetWorkflowByIDResponse contains the result of a workflow retrieval attempt.
type GetWorkflowByIDResponse struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	Id            string                 `protobuf:"bytes,1,opt,name=id,proto3" json:"id,omitempty"`                                          // ID of the workflow
	UserId        string                 `protobuf:"bytes,2,opt,name=user_id,json=userId,proto3" json:"user_id,omitempty"`                    // ID of the user
	Name          string                 `protobuf:"bytes,3,opt,name=name,proto3" json:"name,omitempty"`                                      // Name of the workflow
	Payload       string                 `protobuf:"bytes,4,opt,name=payload,proto3" json:"payload,omitempty"`                                // JSON string for payload
	Kind          string                 `protobuf:"bytes,5,opt,name=kind,proto3" json:"kind,omitempty"`                                      // Kind of workflow
	BuildStatus   string                 `protobuf:"bytes,6,opt,name=build_status,json=buildStatus,proto3" json:"build_status,omitempty"`     // Status of the workflow
	Interval      int32                  `protobuf:"varint,7,opt,name=interval,proto3" json:"interval,omitempty"`                             // Interval measured in minutes
	CreatedAt     string                 `protobuf:"bytes,8,opt,name=created_at,json=createdAt,proto3" json:"created_at,omitempty"`           // Time the workflow was created
	UpdatedAt     string                 `protobuf:"bytes,9,opt,name=updated_at,json=updatedAt,proto3" json:"updated_at,omitempty"`           // Time the workflow was last updated
	TerminatedAt  string                 `protobuf:"bytes,10,opt,name=terminated_at,json=terminatedAt,proto3" json:"terminated_at,omitempty"` // Time the workflow was terminated
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *GetWorkflowByIDResponse) Reset() {
	*x = GetWorkflowByIDResponse{}
	mi := &file_workflows_workflows_proto_msgTypes[9]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *GetWorkflowByIDResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*GetWorkflowByIDResponse) ProtoMessage() {}

func (x *GetWorkflowByIDResponse) ProtoReflect() protoreflect.Message {
	mi := &file_workflows_workflows_proto_msgTypes[9]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use GetWorkflowByIDResponse.ProtoReflect.Descriptor instead.
func (*GetWorkflowByIDResponse) Descriptor() ([]byte, []int) {
	return file_workflows_workflows_proto_rawDescGZIP(), []int{9}
}

func (x *GetWorkflowByIDResponse) GetId() string {
	if x != nil {
		return x.Id
	}
	return ""
}

func (x *GetWorkflowByIDResponse) GetUserId() string {
	if x != nil {
		return x.UserId
	}
	return ""
}

func (x *GetWorkflowByIDResponse) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

func (x *GetWorkflowByIDResponse) GetPayload() string {
	if x != nil {
		return x.Payload
	}
	return ""
}

func (x *GetWorkflowByIDResponse) GetKind() string {
	if x != nil {
		return x.Kind
	}
	return ""
}

func (x *GetWorkflowByIDResponse) GetBuildStatus() string {
	if x != nil {
		return x.BuildStatus
	}
	return ""
}

func (x *GetWorkflowByIDResponse) GetInterval() int32 {
	if x != nil {
		return x.Interval
	}
	return 0
}

func (x *GetWorkflowByIDResponse) GetCreatedAt() string {
	if x != nil {
		return x.CreatedAt
	}
	return ""
}

func (x *GetWorkflowByIDResponse) GetUpdatedAt() string {
	if x != nil {
		return x.UpdatedAt
	}
	return ""
}

func (x *GetWorkflowByIDResponse) GetTerminatedAt() string {
	if x != nil {
		return x.TerminatedAt
	}
	return ""
}

// TerminateWorkflowRequest contains the details needed to terminate a workflow.
type TerminateWorkflowRequest struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	Id            string                 `protobuf:"bytes,1,opt,name=id,proto3" json:"id,omitempty"`                       // ID of the workflow
	UserId        string                 `protobuf:"bytes,2,opt,name=user_id,json=userId,proto3" json:"user_id,omitempty"` // ID of the user
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *TerminateWorkflowRequest) Reset() {
	*x = TerminateWorkflowRequest{}
	mi := &file_workflows_workflows_proto_msgTypes[10]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *TerminateWorkflowRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*TerminateWorkflowRequest) ProtoMessage() {}

func (x *TerminateWorkflowRequest) ProtoReflect() protoreflect.Message {
	mi := &file_workflows_workflows_proto_msgTypes[10]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use TerminateWorkflowRequest.ProtoReflect.Descriptor instead.
func (*TerminateWorkflowRequest) Descriptor() ([]byte, []int) {
	return file_workflows_workflows_proto_rawDescGZIP(), []int{10}
}

func (x *TerminateWorkflowRequest) GetId() string {
	if x != nil {
		return x.Id
	}
	return ""
}

func (x *TerminateWorkflowRequest) GetUserId() string {
	if x != nil {
		return x.UserId
	}
	return ""
}

// ListWorkflowsRequest contains the details needed to list all workflows.
type ListWorkflowsRequest struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	UserId        string                 `protobuf:"bytes,1,opt,name=user_id,json=userId,proto3" json:"user_id,omitempty"` // ID of the user
	Cursor        string                 `protobuf:"bytes,2,opt,name=cursor,proto3" json:"cursor,omitempty"`               // Cursor for pagination
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ListWorkflowsRequest) Reset() {
	*x = ListWorkflowsRequest{}
	mi := &file_workflows_workflows_proto_msgTypes[11]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ListWorkflowsRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ListWorkflowsRequest) ProtoMessage() {}

func (x *ListWorkflowsRequest) ProtoReflect() protoreflect.Message {
	mi := &file_workflows_workflows_proto_msgTypes[11]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ListWorkflowsRequest.ProtoReflect.Descriptor instead.
func (*ListWorkflowsRequest) Descriptor() ([]byte, []int) {
	return file_workflows_workflows_proto_rawDescGZIP(), []int{11}
}

func (x *ListWorkflowsRequest) GetUserId() string {
	if x != nil {
		return x.UserId
	}
	return ""
}

func (x *ListWorkflowsRequest) GetCursor() string {
	if x != nil {
		return x.Cursor
	}
	return ""
}

// WorkflowsByUserIDResponse contains the result of a workflow listing attempt.
type WorkflowsByUserIDResponse struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	Id            string                 `protobuf:"bytes,1,opt,name=id,proto3" json:"id,omitempty"`                                         // ID of the workflow
	Name          string                 `protobuf:"bytes,2,opt,name=name,proto3" json:"name,omitempty"`                                     // Name of the workflow
	Payload       string                 `protobuf:"bytes,3,opt,name=payload,proto3" json:"payload,omitempty"`                               // JSON string for payload
	Kind          string                 `protobuf:"bytes,4,opt,name=kind,proto3" json:"kind,omitempty"`                                     // Kind of workflow
	BuildStatus   string                 `protobuf:"bytes,5,opt,name=build_status,json=buildStatus,proto3" json:"build_status,omitempty"`    // Status of the workflow
	Interval      int32                  `protobuf:"varint,6,opt,name=interval,proto3" json:"interval,omitempty"`                            // Interval measured in minutes
	CreatedAt     string                 `protobuf:"bytes,7,opt,name=created_at,json=createdAt,proto3" json:"created_at,omitempty"`          // Time the workflow was created
	UpdatedAt     string                 `protobuf:"bytes,8,opt,name=updated_at,json=updatedAt,proto3" json:"updated_at,omitempty"`          // Time the workflow was last updated
	TerminatedAt  string                 `protobuf:"bytes,9,opt,name=terminated_at,json=terminatedAt,proto3" json:"terminated_at,omitempty"` // Time the workflow was terminated
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *WorkflowsByUserIDResponse) Reset() {
	*x = WorkflowsByUserIDResponse{}
	mi := &file_workflows_workflows_proto_msgTypes[12]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *WorkflowsByUserIDResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*WorkflowsByUserIDResponse) ProtoMessage() {}

func (x *WorkflowsByUserIDResponse) ProtoReflect() protoreflect.Message {
	mi := &file_workflows_workflows_proto_msgTypes[12]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use WorkflowsByUserIDResponse.ProtoReflect.Descriptor instead.
func (*WorkflowsByUserIDResponse) Descriptor() ([]byte, []int) {
	return file_workflows_workflows_proto_rawDescGZIP(), []int{12}
}

func (x *WorkflowsByUserIDResponse) GetId() string {
	if x != nil {
		return x.Id
	}
	return ""
}

func (x *WorkflowsByUserIDResponse) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

func (x *WorkflowsByUserIDResponse) GetPayload() string {
	if x != nil {
		return x.Payload
	}
	return ""
}

func (x *WorkflowsByUserIDResponse) GetKind() string {
	if x != nil {
		return x.Kind
	}
	return ""
}

func (x *WorkflowsByUserIDResponse) GetBuildStatus() string {
	if x != nil {
		return x.BuildStatus
	}
	return ""
}

func (x *WorkflowsByUserIDResponse) GetInterval() int32 {
	if x != nil {
		return x.Interval
	}
	return 0
}

func (x *WorkflowsByUserIDResponse) GetCreatedAt() string {
	if x != nil {
		return x.CreatedAt
	}
	return ""
}

func (x *WorkflowsByUserIDResponse) GetUpdatedAt() string {
	if x != nil {
		return x.UpdatedAt
	}
	return ""
}

func (x *WorkflowsByUserIDResponse) GetTerminatedAt() string {
	if x != nil {
		return x.TerminatedAt
	}
	return ""
}

// ListWorkflowsResponse contains the result of a workflow listing attempt.
type ListWorkflowsResponse struct {
	state         protoimpl.MessageState       `protogen:"open.v1"`
	Workflows     []*WorkflowsByUserIDResponse `protobuf:"bytes,1,rep,name=workflows,proto3" json:"workflows,omitempty"` // List of workflows
	Cursor        string                       `protobuf:"bytes,2,opt,name=cursor,proto3" json:"cursor,omitempty"`       // Cursor for pagination
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ListWorkflowsResponse) Reset() {
	*x = ListWorkflowsResponse{}
	mi := &file_workflows_workflows_proto_msgTypes[13]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ListWorkflowsResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ListWorkflowsResponse) ProtoMessage() {}

func (x *ListWorkflowsResponse) ProtoReflect() protoreflect.Message {
	mi := &file_workflows_workflows_proto_msgTypes[13]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ListWorkflowsResponse.ProtoReflect.Descriptor instead.
func (*ListWorkflowsResponse) Descriptor() ([]byte, []int) {
	return file_workflows_workflows_proto_rawDescGZIP(), []int{13}
}

func (x *ListWorkflowsResponse) GetWorkflows() []*WorkflowsByUserIDResponse {
	if x != nil {
		return x.Workflows
	}
	return nil
}

func (x *ListWorkflowsResponse) GetCursor() string {
	if x != nil {
		return x.Cursor
	}
	return ""
}

// TerminateWorkflowResponse contains the result of a workflow termination attempt.
type TerminateWorkflowResponse struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *TerminateWorkflowResponse) Reset() {
	*x = TerminateWorkflowResponse{}
	mi := &file_workflows_workflows_proto_msgTypes[14]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *TerminateWorkflowResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*TerminateWorkflowResponse) ProtoMessage() {}

func (x *TerminateWorkflowResponse) ProtoReflect() protoreflect.Message {
	mi := &file_workflows_workflows_proto_msgTypes[14]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use TerminateWorkflowResponse.ProtoReflect.Descriptor instead.
func (*TerminateWorkflowResponse) Descriptor() ([]byte, []int) {
	return file_workflows_workflows_proto_rawDescGZIP(), []int{14}
}

var File_workflows_workflows_proto protoreflect.FileDescriptor

const file_workflows_workflows_proto_rawDesc = "" +
	"\n" +
	"\x19workflows/workflows.proto\x12\tworkflows\"\x8e\x01\n" +
	"\x15CreateWorkflowRequest\x12\x17\n" +
	"\auser_id\x18\x01 \x01(\tR\x06userId\x12\x12\n" +
	"\x04name\x18\x02 \x01(\tR\x04name\x12\x18\n" +
	"\apayload\x18\x03 \x01(\tR\apayload\x12\x12\n" +
	"\x04kind\x18\x04 \x01(\tR\x04kind\x12\x1a\n" +
	"\binterval\x18\x05 \x01(\x05R\binterval\"(\n" +
	"\x16CreateWorkflowResponse\x12\x0e\n" +
	"\x02id\x18\x01 \x01(\tR\x02id\"\x8a\x01\n" +
	"\x15UpdateWorkflowRequest\x12\x0e\n" +
	"\x02id\x18\x01 \x01(\tR\x02id\x12\x17\n" +
	"\auser_id\x18\x02 \x01(\tR\x06userId\x12\x12\n" +
	"\x04name\x18\x03 \x01(\tR\x04name\x12\x18\n" +
	"\apayload\x18\x04 \x01(\tR\apayload\x12\x1a\n" +
	"\binterval\x18\x05 \x01(\x05R\binterval\"\x18\n" +
	"\x16UpdateWorkflowResponse\"U\n" +
	" UpdateWorkflowBuildStatusRequest\x12\x0e\n" +
	"\x02id\x18\x01 \x01(\tR\x02id\x12!\n" +
	"\fbuild_status\x18\x02 \x01(\tR\vbuildStatus\"#\n" +
	"!UpdateWorkflowBuildStatusResponse\"=\n" +
	"\x12GetWorkflowRequest\x12\x0e\n" +
	"\x02id\x18\x01 \x01(\tR\x02id\x12\x17\n" +
	"\auser_id\x18\x02 \x01(\tR\x06userId\"\x89\x02\n" +
	"\x13GetWorkflowResponse\x12\x0e\n" +
	"\x02id\x18\x01 \x01(\tR\x02id\x12\x12\n" +
	"\x04name\x18\x02 \x01(\tR\x04name\x12\x18\n" +
	"\apayload\x18\x03 \x01(\tR\apayload\x12\x12\n" +
	"\x04kind\x18\x04 \x01(\tR\x04kind\x12!\n" +
	"\fbuild_status\x18\x05 \x01(\tR\vbuildStatus\x12\x1a\n" +
	"\binterval\x18\x06 \x01(\x05R\binterval\x12\x1d\n" +
	"\n" +
	"created_at\x18\a \x01(\tR\tcreatedAt\x12\x1d\n" +
	"\n" +
	"updated_at\x18\b \x01(\tR\tupdatedAt\x12#\n" +
	"\rterminated_at\x18\t \x01(\tR\fterminatedAt\"(\n" +
	"\x16GetWorkflowByIDRequest\x12\x0e\n" +
	"\x02id\x18\x01 \x01(\tR\x02id\"\xa6\x02\n" +
	"\x17GetWorkflowByIDResponse\x12\x0e\n" +
	"\x02id\x18\x01 \x01(\tR\x02id\x12\x17\n" +
	"\auser_id\x18\x02 \x01(\tR\x06userId\x12\x12\n" +
	"\x04name\x18\x03 \x01(\tR\x04name\x12\x18\n" +
	"\apayload\x18\x04 \x01(\tR\apayload\x12\x12\n" +
	"\x04kind\x18\x05 \x01(\tR\x04kind\x12!\n" +
	"\fbuild_status\x18\x06 \x01(\tR\vbuildStatus\x12\x1a\n" +
	"\binterval\x18\a \x01(\x05R\binterval\x12\x1d\n" +
	"\n" +
	"created_at\x18\b \x01(\tR\tcreatedAt\x12\x1d\n" +
	"\n" +
	"updated_at\x18\t \x01(\tR\tupdatedAt\x12#\n" +
	"\rterminated_at\x18\n" +
	" \x01(\tR\fterminatedAt\"C\n" +
	"\x18TerminateWorkflowRequest\x12\x0e\n" +
	"\x02id\x18\x01 \x01(\tR\x02id\x12\x17\n" +
	"\auser_id\x18\x02 \x01(\tR\x06userId\"G\n" +
	"\x14ListWorkflowsRequest\x12\x17\n" +
	"\auser_id\x18\x01 \x01(\tR\x06userId\x12\x16\n" +
	"\x06cursor\x18\x02 \x01(\tR\x06cursor\"\x8f\x02\n" +
	"\x19WorkflowsByUserIDResponse\x12\x0e\n" +
	"\x02id\x18\x01 \x01(\tR\x02id\x12\x12\n" +
	"\x04name\x18\x02 \x01(\tR\x04name\x12\x18\n" +
	"\apayload\x18\x03 \x01(\tR\apayload\x12\x12\n" +
	"\x04kind\x18\x04 \x01(\tR\x04kind\x12!\n" +
	"\fbuild_status\x18\x05 \x01(\tR\vbuildStatus\x12\x1a\n" +
	"\binterval\x18\x06 \x01(\x05R\binterval\x12\x1d\n" +
	"\n" +
	"created_at\x18\a \x01(\tR\tcreatedAt\x12\x1d\n" +
	"\n" +
	"updated_at\x18\b \x01(\tR\tupdatedAt\x12#\n" +
	"\rterminated_at\x18\t \x01(\tR\fterminatedAt\"s\n" +
	"\x15ListWorkflowsResponse\x12B\n" +
	"\tworkflows\x18\x01 \x03(\v2$.workflows.WorkflowsByUserIDResponseR\tworkflows\x12\x16\n" +
	"\x06cursor\x18\x02 \x01(\tR\x06cursor\"\x1b\n" +
	"\x19TerminateWorkflowResponse2\xa2\x05\n" +
	"\x10WorkflowsService\x12W\n" +
	"\x0eCreateWorkflow\x12 .workflows.CreateWorkflowRequest\x1a!.workflows.CreateWorkflowResponse\"\x00\x12W\n" +
	"\x0eUpdateWorkflow\x12 .workflows.UpdateWorkflowRequest\x1a!.workflows.UpdateWorkflowResponse\"\x00\x12x\n" +
	"\x19UpdateWorkflowBuildStatus\x12+.workflows.UpdateWorkflowBuildStatusRequest\x1a,.workflows.UpdateWorkflowBuildStatusResponse\"\x00\x12N\n" +
	"\vGetWorkflow\x12\x1d.workflows.GetWorkflowRequest\x1a\x1e.workflows.GetWorkflowResponse\"\x00\x12Z\n" +
	"\x0fGetWorkflowByID\x12!.workflows.GetWorkflowByIDRequest\x1a\".workflows.GetWorkflowByIDResponse\"\x00\x12`\n" +
	"\x11TerminateWorkflow\x12#.workflows.TerminateWorkflowRequest\x1a$.workflows.TerminateWorkflowResponse\"\x00\x12T\n" +
	"\rListWorkflows\x12\x1f.workflows.ListWorkflowsRequest\x1a .workflows.ListWorkflowsResponse\"\x00BBZ@github.com/hitesh22rana/chronoverse/proto/go/workflows;workflowsb\x06proto3"

var (
	file_workflows_workflows_proto_rawDescOnce sync.Once
	file_workflows_workflows_proto_rawDescData []byte
)

func file_workflows_workflows_proto_rawDescGZIP() []byte {
	file_workflows_workflows_proto_rawDescOnce.Do(func() {
		file_workflows_workflows_proto_rawDescData = protoimpl.X.CompressGZIP(unsafe.Slice(unsafe.StringData(file_workflows_workflows_proto_rawDesc), len(file_workflows_workflows_proto_rawDesc)))
	})
	return file_workflows_workflows_proto_rawDescData
}

var file_workflows_workflows_proto_msgTypes = make([]protoimpl.MessageInfo, 15)
var file_workflows_workflows_proto_goTypes = []any{
	(*CreateWorkflowRequest)(nil),             // 0: workflows.CreateWorkflowRequest
	(*CreateWorkflowResponse)(nil),            // 1: workflows.CreateWorkflowResponse
	(*UpdateWorkflowRequest)(nil),             // 2: workflows.UpdateWorkflowRequest
	(*UpdateWorkflowResponse)(nil),            // 3: workflows.UpdateWorkflowResponse
	(*UpdateWorkflowBuildStatusRequest)(nil),  // 4: workflows.UpdateWorkflowBuildStatusRequest
	(*UpdateWorkflowBuildStatusResponse)(nil), // 5: workflows.UpdateWorkflowBuildStatusResponse
	(*GetWorkflowRequest)(nil),                // 6: workflows.GetWorkflowRequest
	(*GetWorkflowResponse)(nil),               // 7: workflows.GetWorkflowResponse
	(*GetWorkflowByIDRequest)(nil),            // 8: workflows.GetWorkflowByIDRequest
	(*GetWorkflowByIDResponse)(nil),           // 9: workflows.GetWorkflowByIDResponse
	(*TerminateWorkflowRequest)(nil),          // 10: workflows.TerminateWorkflowRequest
	(*ListWorkflowsRequest)(nil),              // 11: workflows.ListWorkflowsRequest
	(*WorkflowsByUserIDResponse)(nil),         // 12: workflows.WorkflowsByUserIDResponse
	(*ListWorkflowsResponse)(nil),             // 13: workflows.ListWorkflowsResponse
	(*TerminateWorkflowResponse)(nil),         // 14: workflows.TerminateWorkflowResponse
}
var file_workflows_workflows_proto_depIdxs = []int32{
	12, // 0: workflows.ListWorkflowsResponse.workflows:type_name -> workflows.WorkflowsByUserIDResponse
	0,  // 1: workflows.WorkflowsService.CreateWorkflow:input_type -> workflows.CreateWorkflowRequest
	2,  // 2: workflows.WorkflowsService.UpdateWorkflow:input_type -> workflows.UpdateWorkflowRequest
	4,  // 3: workflows.WorkflowsService.UpdateWorkflowBuildStatus:input_type -> workflows.UpdateWorkflowBuildStatusRequest
	6,  // 4: workflows.WorkflowsService.GetWorkflow:input_type -> workflows.GetWorkflowRequest
	8,  // 5: workflows.WorkflowsService.GetWorkflowByID:input_type -> workflows.GetWorkflowByIDRequest
	10, // 6: workflows.WorkflowsService.TerminateWorkflow:input_type -> workflows.TerminateWorkflowRequest
	11, // 7: workflows.WorkflowsService.ListWorkflows:input_type -> workflows.ListWorkflowsRequest
	1,  // 8: workflows.WorkflowsService.CreateWorkflow:output_type -> workflows.CreateWorkflowResponse
	3,  // 9: workflows.WorkflowsService.UpdateWorkflow:output_type -> workflows.UpdateWorkflowResponse
	5,  // 10: workflows.WorkflowsService.UpdateWorkflowBuildStatus:output_type -> workflows.UpdateWorkflowBuildStatusResponse
	7,  // 11: workflows.WorkflowsService.GetWorkflow:output_type -> workflows.GetWorkflowResponse
	9,  // 12: workflows.WorkflowsService.GetWorkflowByID:output_type -> workflows.GetWorkflowByIDResponse
	14, // 13: workflows.WorkflowsService.TerminateWorkflow:output_type -> workflows.TerminateWorkflowResponse
	13, // 14: workflows.WorkflowsService.ListWorkflows:output_type -> workflows.ListWorkflowsResponse
	8,  // [8:15] is the sub-list for method output_type
	1,  // [1:8] is the sub-list for method input_type
	1,  // [1:1] is the sub-list for extension type_name
	1,  // [1:1] is the sub-list for extension extendee
	0,  // [0:1] is the sub-list for field type_name
}

func init() { file_workflows_workflows_proto_init() }
func file_workflows_workflows_proto_init() {
	if File_workflows_workflows_proto != nil {
		return
	}
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: unsafe.Slice(unsafe.StringData(file_workflows_workflows_proto_rawDesc), len(file_workflows_workflows_proto_rawDesc)),
			NumEnums:      0,
			NumMessages:   15,
			NumExtensions: 0,
			NumServices:   1,
		},
		GoTypes:           file_workflows_workflows_proto_goTypes,
		DependencyIndexes: file_workflows_workflows_proto_depIdxs,
		MessageInfos:      file_workflows_workflows_proto_msgTypes,
	}.Build()
	File_workflows_workflows_proto = out.File
	file_workflows_workflows_proto_goTypes = nil
	file_workflows_workflows_proto_depIdxs = nil
}
