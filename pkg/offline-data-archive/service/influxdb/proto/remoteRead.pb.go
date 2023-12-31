// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.28.1
// 	protoc        v4.22.5
// source: remoteRead.proto

package remoteRead

import (
	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	protoimpl "google.golang.org/protobuf/runtime/protoimpl"
	reflect "reflect"
	sync "sync"
)

const (
	// Verify that this generated code is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(20 - protoimpl.MinVersion)
	// Verify that runtime/protoimpl is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(protoimpl.MaxVersion - 20)
)

type ReadRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	ClusterName string `protobuf:"bytes,1,opt,name=clusterName,proto3" json:"clusterName,omitempty"`
	TagRouter   string `protobuf:"bytes,2,opt,name=tagRouter,proto3" json:"tagRouter,omitempty"`
	Db          string `protobuf:"bytes,3,opt,name=db,proto3" json:"db,omitempty"`
	Rp          string `protobuf:"bytes,4,opt,name=rp,proto3" json:"rp,omitempty"`
	Measurement string `protobuf:"bytes,5,opt,name=measurement,proto3" json:"measurement,omitempty"`
	Field       string `protobuf:"bytes,6,opt,name=field,proto3" json:"field,omitempty"`
	Start       int64  `protobuf:"varint,7,opt,name=start,proto3" json:"start,omitempty"`
	End         int64  `protobuf:"varint,8,opt,name=end,proto3" json:"end,omitempty"`
	Condition   string `protobuf:"bytes,9,opt,name=condition,proto3" json:"condition,omitempty"`
	Limit       int64  `protobuf:"varint,10,opt,name=limit,proto3" json:"limit,omitempty"`
	SLimit      int64  `protobuf:"varint,11,opt,name=sLimit,proto3" json:"sLimit,omitempty"`
}

func (x *ReadRequest) Reset() {
	*x = ReadRequest{}
	if protoimpl.UnsafeEnabled {
		mi := &file_remoteRead_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *ReadRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ReadRequest) ProtoMessage() {}

func (x *ReadRequest) ProtoReflect() protoreflect.Message {
	mi := &file_remoteRead_proto_msgTypes[0]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ReadRequest.ProtoReflect.Descriptor instead.
func (*ReadRequest) Descriptor() ([]byte, []int) {
	return file_remoteRead_proto_rawDescGZIP(), []int{0}
}

func (x *ReadRequest) GetClusterName() string {
	if x != nil {
		return x.ClusterName
	}
	return ""
}

func (x *ReadRequest) GetTagRouter() string {
	if x != nil {
		return x.TagRouter
	}
	return ""
}

func (x *ReadRequest) GetDb() string {
	if x != nil {
		return x.Db
	}
	return ""
}

func (x *ReadRequest) GetRp() string {
	if x != nil {
		return x.Rp
	}
	return ""
}

func (x *ReadRequest) GetMeasurement() string {
	if x != nil {
		return x.Measurement
	}
	return ""
}

func (x *ReadRequest) GetField() string {
	if x != nil {
		return x.Field
	}
	return ""
}

func (x *ReadRequest) GetStart() int64 {
	if x != nil {
		return x.Start
	}
	return 0
}

func (x *ReadRequest) GetEnd() int64 {
	if x != nil {
		return x.End
	}
	return 0
}

func (x *ReadRequest) GetCondition() string {
	if x != nil {
		return x.Condition
	}
	return ""
}

func (x *ReadRequest) GetLimit() int64 {
	if x != nil {
		return x.Limit
	}
	return 0
}

func (x *ReadRequest) GetSLimit() int64 {
	if x != nil {
		return x.SLimit
	}
	return 0
}

type Sample struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Value       float64 `protobuf:"fixed64,1,opt,name=value,proto3" json:"value,omitempty"`
	TimestampMs int64   `protobuf:"varint,2,opt,name=timestamp_ms,json=timestampMs,proto3" json:"timestamp_ms,omitempty"`
}

func (x *Sample) Reset() {
	*x = Sample{}
	if protoimpl.UnsafeEnabled {
		mi := &file_remoteRead_proto_msgTypes[1]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *Sample) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Sample) ProtoMessage() {}

func (x *Sample) ProtoReflect() protoreflect.Message {
	mi := &file_remoteRead_proto_msgTypes[1]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Sample.ProtoReflect.Descriptor instead.
func (*Sample) Descriptor() ([]byte, []int) {
	return file_remoteRead_proto_rawDescGZIP(), []int{1}
}

func (x *Sample) GetValue() float64 {
	if x != nil {
		return x.Value
	}
	return 0
}

func (x *Sample) GetTimestampMs() int64 {
	if x != nil {
		return x.TimestampMs
	}
	return 0
}

type LabelPair struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Name  string `protobuf:"bytes,1,opt,name=name,proto3" json:"name,omitempty"`
	Value string `protobuf:"bytes,2,opt,name=value,proto3" json:"value,omitempty"`
}

func (x *LabelPair) Reset() {
	*x = LabelPair{}
	if protoimpl.UnsafeEnabled {
		mi := &file_remoteRead_proto_msgTypes[2]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *LabelPair) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*LabelPair) ProtoMessage() {}

func (x *LabelPair) ProtoReflect() protoreflect.Message {
	mi := &file_remoteRead_proto_msgTypes[2]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use LabelPair.ProtoReflect.Descriptor instead.
func (*LabelPair) Descriptor() ([]byte, []int) {
	return file_remoteRead_proto_rawDescGZIP(), []int{2}
}

func (x *LabelPair) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

func (x *LabelPair) GetValue() string {
	if x != nil {
		return x.Value
	}
	return ""
}

type TimeSeries struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Labels []*LabelPair `protobuf:"bytes,1,rep,name=labels,proto3" json:"labels,omitempty"`
	// Sorted by time, oldest sample first.
	Samples []*Sample `protobuf:"bytes,2,rep,name=samples,proto3" json:"samples,omitempty"`
}

func (x *TimeSeries) Reset() {
	*x = TimeSeries{}
	if protoimpl.UnsafeEnabled {
		mi := &file_remoteRead_proto_msgTypes[3]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *TimeSeries) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*TimeSeries) ProtoMessage() {}

func (x *TimeSeries) ProtoReflect() protoreflect.Message {
	mi := &file_remoteRead_proto_msgTypes[3]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use TimeSeries.ProtoReflect.Descriptor instead.
func (*TimeSeries) Descriptor() ([]byte, []int) {
	return file_remoteRead_proto_rawDescGZIP(), []int{3}
}

func (x *TimeSeries) GetLabels() []*LabelPair {
	if x != nil {
		return x.Labels
	}
	return nil
}

func (x *TimeSeries) GetSamples() []*Sample {
	if x != nil {
		return x.Samples
	}
	return nil
}

var File_remoteRead_proto protoreflect.FileDescriptor

var file_remoteRead_proto_rawDesc = []byte{
	0x0a, 0x10, 0x72, 0x65, 0x6d, 0x6f, 0x74, 0x65, 0x52, 0x65, 0x61, 0x64, 0x2e, 0x70, 0x72, 0x6f,
	0x74, 0x6f, 0x12, 0x0a, 0x72, 0x65, 0x6d, 0x6f, 0x74, 0x65, 0x52, 0x65, 0x61, 0x64, 0x22, 0x99,
	0x02, 0x0a, 0x0b, 0x52, 0x65, 0x61, 0x64, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x12, 0x20,
	0x0a, 0x0b, 0x63, 0x6c, 0x75, 0x73, 0x74, 0x65, 0x72, 0x4e, 0x61, 0x6d, 0x65, 0x18, 0x01, 0x20,
	0x01, 0x28, 0x09, 0x52, 0x0b, 0x63, 0x6c, 0x75, 0x73, 0x74, 0x65, 0x72, 0x4e, 0x61, 0x6d, 0x65,
	0x12, 0x1c, 0x0a, 0x09, 0x74, 0x61, 0x67, 0x52, 0x6f, 0x75, 0x74, 0x65, 0x72, 0x18, 0x02, 0x20,
	0x01, 0x28, 0x09, 0x52, 0x09, 0x74, 0x61, 0x67, 0x52, 0x6f, 0x75, 0x74, 0x65, 0x72, 0x12, 0x0e,
	0x0a, 0x02, 0x64, 0x62, 0x18, 0x03, 0x20, 0x01, 0x28, 0x09, 0x52, 0x02, 0x64, 0x62, 0x12, 0x0e,
	0x0a, 0x02, 0x72, 0x70, 0x18, 0x04, 0x20, 0x01, 0x28, 0x09, 0x52, 0x02, 0x72, 0x70, 0x12, 0x20,
	0x0a, 0x0b, 0x6d, 0x65, 0x61, 0x73, 0x75, 0x72, 0x65, 0x6d, 0x65, 0x6e, 0x74, 0x18, 0x05, 0x20,
	0x01, 0x28, 0x09, 0x52, 0x0b, 0x6d, 0x65, 0x61, 0x73, 0x75, 0x72, 0x65, 0x6d, 0x65, 0x6e, 0x74,
	0x12, 0x14, 0x0a, 0x05, 0x66, 0x69, 0x65, 0x6c, 0x64, 0x18, 0x06, 0x20, 0x01, 0x28, 0x09, 0x52,
	0x05, 0x66, 0x69, 0x65, 0x6c, 0x64, 0x12, 0x14, 0x0a, 0x05, 0x73, 0x74, 0x61, 0x72, 0x74, 0x18,
	0x07, 0x20, 0x01, 0x28, 0x03, 0x52, 0x05, 0x73, 0x74, 0x61, 0x72, 0x74, 0x12, 0x10, 0x0a, 0x03,
	0x65, 0x6e, 0x64, 0x18, 0x08, 0x20, 0x01, 0x28, 0x03, 0x52, 0x03, 0x65, 0x6e, 0x64, 0x12, 0x1c,
	0x0a, 0x09, 0x63, 0x6f, 0x6e, 0x64, 0x69, 0x74, 0x69, 0x6f, 0x6e, 0x18, 0x09, 0x20, 0x01, 0x28,
	0x09, 0x52, 0x09, 0x63, 0x6f, 0x6e, 0x64, 0x69, 0x74, 0x69, 0x6f, 0x6e, 0x12, 0x14, 0x0a, 0x05,
	0x6c, 0x69, 0x6d, 0x69, 0x74, 0x18, 0x0a, 0x20, 0x01, 0x28, 0x03, 0x52, 0x05, 0x6c, 0x69, 0x6d,
	0x69, 0x74, 0x12, 0x16, 0x0a, 0x06, 0x73, 0x4c, 0x69, 0x6d, 0x69, 0x74, 0x18, 0x0b, 0x20, 0x01,
	0x28, 0x03, 0x52, 0x06, 0x73, 0x4c, 0x69, 0x6d, 0x69, 0x74, 0x22, 0x41, 0x0a, 0x06, 0x53, 0x61,
	0x6d, 0x70, 0x6c, 0x65, 0x12, 0x14, 0x0a, 0x05, 0x76, 0x61, 0x6c, 0x75, 0x65, 0x18, 0x01, 0x20,
	0x01, 0x28, 0x01, 0x52, 0x05, 0x76, 0x61, 0x6c, 0x75, 0x65, 0x12, 0x21, 0x0a, 0x0c, 0x74, 0x69,
	0x6d, 0x65, 0x73, 0x74, 0x61, 0x6d, 0x70, 0x5f, 0x6d, 0x73, 0x18, 0x02, 0x20, 0x01, 0x28, 0x03,
	0x52, 0x0b, 0x74, 0x69, 0x6d, 0x65, 0x73, 0x74, 0x61, 0x6d, 0x70, 0x4d, 0x73, 0x22, 0x35, 0x0a,
	0x09, 0x4c, 0x61, 0x62, 0x65, 0x6c, 0x50, 0x61, 0x69, 0x72, 0x12, 0x12, 0x0a, 0x04, 0x6e, 0x61,
	0x6d, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x12, 0x14,
	0x0a, 0x05, 0x76, 0x61, 0x6c, 0x75, 0x65, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09, 0x52, 0x05, 0x76,
	0x61, 0x6c, 0x75, 0x65, 0x22, 0x69, 0x0a, 0x0a, 0x54, 0x69, 0x6d, 0x65, 0x53, 0x65, 0x72, 0x69,
	0x65, 0x73, 0x12, 0x2d, 0x0a, 0x06, 0x6c, 0x61, 0x62, 0x65, 0x6c, 0x73, 0x18, 0x01, 0x20, 0x03,
	0x28, 0x0b, 0x32, 0x15, 0x2e, 0x72, 0x65, 0x6d, 0x6f, 0x74, 0x65, 0x52, 0x65, 0x61, 0x64, 0x2e,
	0x4c, 0x61, 0x62, 0x65, 0x6c, 0x50, 0x61, 0x69, 0x72, 0x52, 0x06, 0x6c, 0x61, 0x62, 0x65, 0x6c,
	0x73, 0x12, 0x2c, 0x0a, 0x07, 0x73, 0x61, 0x6d, 0x70, 0x6c, 0x65, 0x73, 0x18, 0x02, 0x20, 0x03,
	0x28, 0x0b, 0x32, 0x12, 0x2e, 0x72, 0x65, 0x6d, 0x6f, 0x74, 0x65, 0x52, 0x65, 0x61, 0x64, 0x2e,
	0x53, 0x61, 0x6d, 0x70, 0x6c, 0x65, 0x52, 0x07, 0x73, 0x61, 0x6d, 0x70, 0x6c, 0x65, 0x73, 0x32,
	0x54, 0x0a, 0x16, 0x51, 0x75, 0x65, 0x72, 0x79, 0x54, 0x69, 0x6d, 0x65, 0x53, 0x65, 0x72, 0x69,
	0x65, 0x73, 0x53, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x12, 0x3a, 0x0a, 0x03, 0x52, 0x61, 0x77,
	0x12, 0x17, 0x2e, 0x72, 0x65, 0x6d, 0x6f, 0x74, 0x65, 0x52, 0x65, 0x61, 0x64, 0x2e, 0x52, 0x65,
	0x61, 0x64, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x16, 0x2e, 0x72, 0x65, 0x6d, 0x6f,
	0x74, 0x65, 0x52, 0x65, 0x61, 0x64, 0x2e, 0x54, 0x69, 0x6d, 0x65, 0x53, 0x65, 0x72, 0x69, 0x65,
	0x73, 0x22, 0x00, 0x30, 0x01, 0x42, 0x0f, 0x5a, 0x0d, 0x2e, 0x2f, 0x3b, 0x72, 0x65, 0x6d, 0x6f,
	0x74, 0x65, 0x52, 0x65, 0x61, 0x64, 0x62, 0x06, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x33,
}

var (
	file_remoteRead_proto_rawDescOnce sync.Once
	file_remoteRead_proto_rawDescData = file_remoteRead_proto_rawDesc
)

func file_remoteRead_proto_rawDescGZIP() []byte {
	file_remoteRead_proto_rawDescOnce.Do(func() {
		file_remoteRead_proto_rawDescData = protoimpl.X.CompressGZIP(file_remoteRead_proto_rawDescData)
	})
	return file_remoteRead_proto_rawDescData
}

var file_remoteRead_proto_msgTypes = make([]protoimpl.MessageInfo, 4)
var file_remoteRead_proto_goTypes = []interface{}{
	(*ReadRequest)(nil), // 0: remoteRead.ReadRequest
	(*Sample)(nil),      // 1: remoteRead.Sample
	(*LabelPair)(nil),   // 2: remoteRead.LabelPair
	(*TimeSeries)(nil),  // 3: remoteRead.TimeSeries
}
var file_remoteRead_proto_depIdxs = []int32{
	2, // 0: remoteRead.TimeSeries.labels:type_name -> remoteRead.LabelPair
	1, // 1: remoteRead.TimeSeries.samples:type_name -> remoteRead.Sample
	0, // 2: remoteRead.QueryTimeSeriesService.Raw:input_type -> remoteRead.ReadRequest
	3, // 3: remoteRead.QueryTimeSeriesService.Raw:output_type -> remoteRead.TimeSeries
	3, // [3:4] is the sub-list for method output_type
	2, // [2:3] is the sub-list for method input_type
	2, // [2:2] is the sub-list for extension type_name
	2, // [2:2] is the sub-list for extension extendee
	0, // [0:2] is the sub-list for field type_name
}

func init() { file_remoteRead_proto_init() }
func file_remoteRead_proto_init() {
	if File_remoteRead_proto != nil {
		return
	}
	if !protoimpl.UnsafeEnabled {
		file_remoteRead_proto_msgTypes[0].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*ReadRequest); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_remoteRead_proto_msgTypes[1].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*Sample); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_remoteRead_proto_msgTypes[2].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*LabelPair); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_remoteRead_proto_msgTypes[3].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*TimeSeries); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
	}
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: file_remoteRead_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   4,
			NumExtensions: 0,
			NumServices:   1,
		},
		GoTypes:           file_remoteRead_proto_goTypes,
		DependencyIndexes: file_remoteRead_proto_depIdxs,
		MessageInfos:      file_remoteRead_proto_msgTypes,
	}.Build()
	File_remoteRead_proto = out.File
	file_remoteRead_proto_rawDesc = nil
	file_remoteRead_proto_goTypes = nil
	file_remoteRead_proto_depIdxs = nil
}
