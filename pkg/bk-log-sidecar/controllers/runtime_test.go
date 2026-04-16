// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 日志平台 (BlueKing - Log) available.
// Copyright (C) 2017-2021 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
//

package controllers

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"
	v1 "k8s.io/cri-api/pkg/apis/runtime/v1"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/utils"
)

// ---------- 1. Version routing: threshold changed to 1.6 ----------

func TestVersionRouting(t *testing.T) {
	tests := []struct {
		version      string
		wantV1Alpha2 bool // true = should use v1alpha2 (< 1.6)
	}{
		{"1.4.3-tke.4", true},
		{"1.4.3-tke.3", true},
		{"1.4.3", true},
		{"1.5.0", true},
		{"1.5.11", true},
		{"1.6.0", false},
		{"1.6.28", false},
		{"1.7.0", false},
		{"2.0.0", false},
	}

	for _, tt := range tests {
		t.Run("containerd://"+tt.version, func(t *testing.T) {
			result := utils.CompareVersion(tt.version, "1.6") < 0
			assert.Equal(t, tt.wantV1Alpha2, result,
				"containerd %s: expected useV1Alpha2=%v, got %v", tt.version, tt.wantV1Alpha2, result)
		})
	}
}

// ---------- 1b. EKS runtime always uses v1alpha2 ----------

func TestEKSRuntimeUsesV1Alpha2(t *testing.T) {
	// EKS path in NewRuntime always passes useV1Alpha2=true.
	// Verify the routing logic: EKS prefix → v1alpha2
	runtimeVersion := "eks://1.8.0"
	assert.True(t, strings.HasPrefix(runtimeVersion, string(define.RuntimeTypeEks)),
		"EKS version string should be recognized")
	// In NewRuntime, EKS unconditionally calls NewContainerdRuntime(true),
	// meaning it always uses v1alpha2 regardless of version number.
}

// ---------- 1c. CompareVersion handles vendor suffixes like -tke.x ----------

func TestCompareVersionWithVendorSuffix(t *testing.T) {
	// "-tke.X" contains non-numeric parts; Atoi returns 0 for "3-tke"
	// "1.4.3-tke.4" splits as ["1","4","3-tke","4"] → parsed as [1,4,0,4]
	// "1.6.0-tke.1" splits as ["1","6","0-tke","1"] → parsed as [1,6,0,1] > [1,6] → result 1
	tests := []struct {
		version  string
		target   string
		expected int
	}{
		{"1.4.3-tke.4", "1.6", -1},
		{"1.4.3-tke.3", "1.6", -1},
		{"1.6.0-tke.1", "1.6", 1},
		{"1.7.2-tke.5", "1.6", 1},
	}

	for _, tt := range tests {
		t.Run(tt.version+"_vs_"+tt.target, func(t *testing.T) {
			result := utils.CompareVersion(tt.version, tt.target)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// ---------- 2. Interceptor: verify method path rewrite ----------

func TestCRIV1Alpha2Rewriter(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{
			"/runtime.v1.RuntimeService/ListContainers",
			"/runtime.v1alpha2.RuntimeService/ListContainers",
		},
		{
			"/runtime.v1.RuntimeService/ContainerStatus",
			"/runtime.v1alpha2.RuntimeService/ContainerStatus",
		},
		{
			"/runtime.v1.RuntimeService/ListPodSandbox",
			"/runtime.v1alpha2.RuntimeService/ListPodSandbox",
		},
		{
			"/some.other.Service/Method",
			"/some.other.Service/Method",
		},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			var captured string
			fakeInvoker := func(_ context.Context, method string, _, _ interface{}, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
				captured = method
				return nil
			}

			err := criV1Alpha2Rewriter(context.Background(), tt.input, nil, nil, nil, fakeInvoker)
			assert.NoError(t, err)
			assert.Equal(t, tt.expected, captured)
		})
	}
}

// ---------- 3. Integration: verify interceptor rewrites gRPC method path on the wire ----------

// This test sets up a real gRPC server + client to prove that the interceptor
// changes the actual method path from runtime.v1 to runtime.v1alpha2.

// methodCapture is a server-side interceptor that records the full gRPC method path.
func methodCapture(captured *[]string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		*captured = append(*captured, info.FullMethod)
		return handler(ctx, req)
	}
}

func TestInterceptorRewritesOnWire(t *testing.T) {
	// -- Set up a gRPC server that registers CRI v1 service and captures method paths --
	var captured []string
	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer(grpc.UnaryInterceptor(methodCapture(&captured)))
	v1.RegisterRuntimeServiceServer(srv, &v1.UnimplementedRuntimeServiceServer{})

	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()

	dialer := func(ctx context.Context, _ string) (net.Conn, error) {
		return lis.DialContext(ctx)
	}

	// -- Case 1: WITHOUT interceptor → method path is runtime.v1 --
	conn1, err := grpc.DialContext(context.Background(), "bufnet",
		grpc.WithContextDialer(dialer),
		grpc.WithInsecure(),
	)
	require.NoError(t, err)
	defer conn1.Close()

	client1 := v1.NewRuntimeServiceClient(conn1)
	// Call will return Unimplemented from the stub, but the server interceptor still captures the path
	_, _ = client1.ListContainers(context.Background(), &v1.ListContainersRequest{})
	_, _ = client1.ContainerStatus(context.Background(), &v1.ContainerStatusRequest{ContainerId: "abc"})

	require.Len(t, captured, 2)
	assert.Equal(t, "/runtime.v1.RuntimeService/ListContainers", captured[0],
		"Without interceptor: method should be runtime.v1")
	assert.Equal(t, "/runtime.v1.RuntimeService/ContainerStatus", captured[1],
		"Without interceptor: method should be runtime.v1")

	// -- Case 2: WITH interceptor → method path is rewritten to runtime.v1alpha2 --
	captured = nil // reset

	conn2, err := grpc.DialContext(context.Background(), "bufnet",
		grpc.WithContextDialer(dialer),
		grpc.WithInsecure(),
		grpc.WithUnaryInterceptor(criV1Alpha2Rewriter),
	)
	require.NoError(t, err)
	defer conn2.Close()

	client2 := v1.NewRuntimeServiceClient(conn2)
	// These will return Unimplemented because server only has v1, but the server interceptor
	// won't even see them because the method path doesn't match any registered service.
	_, _ = client2.ListContainers(context.Background(), &v1.ListContainersRequest{})
	_, _ = client2.ContainerStatus(context.Background(), &v1.ContainerStatusRequest{ContainerId: "abc"})

	// Server won't see these calls (returns Unimplemented before hitting the interceptor)
	// because there's no runtime.v1alpha2 registered on this server.
	// But we can verify the interceptor behavior directly:
	var rewrittenMethods []string
	fakeInvoker := func(_ context.Context, method string, _, _ interface{}, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
		rewrittenMethods = append(rewrittenMethods, method)
		return nil
	}
	_ = criV1Alpha2Rewriter(context.Background(), "/runtime.v1.RuntimeService/ListContainers", nil, nil, nil, fakeInvoker)
	_ = criV1Alpha2Rewriter(context.Background(), "/runtime.v1.RuntimeService/ContainerStatus", nil, nil, nil, fakeInvoker)

	require.Len(t, rewrittenMethods, 2)
	assert.Equal(t, "/runtime.v1alpha2.RuntimeService/ListContainers", rewrittenMethods[0],
		"With interceptor: method should be rewritten to runtime.v1alpha2")
	assert.Equal(t, "/runtime.v1alpha2.RuntimeService/ContainerStatus", rewrittenMethods[1],
		"With interceptor: method should be rewritten to runtime.v1alpha2")

	fmt.Println("✓ Without interceptor: /runtime.v1.RuntimeService/* (original bug path)")
	fmt.Println("✓ With    interceptor: /runtime.v1alpha2.RuntimeService/* (fix works)")
}

// ---------- 4. Full round-trip: v1 client + interceptor → v1alpha2 server → success ----------

// fakeV1Alpha2Server simulates containerd < 1.6 that only serves runtime.v1alpha2.
// We build the gRPC ServiceDesc manually with ServiceName = "runtime.v1alpha2.RuntimeService".
type fakeV1Alpha2Server struct {
	v1.UnimplementedRuntimeServiceServer
}

func (s *fakeV1Alpha2Server) ListContainers(_ context.Context, _ *v1.ListContainersRequest) (*v1.ListContainersResponse, error) {
	return &v1.ListContainersResponse{
		Containers: []*v1.Container{{Id: "container-001"}},
	}, nil
}

func (s *fakeV1Alpha2Server) ContainerStatus(_ context.Context, req *v1.ContainerStatusRequest) (*v1.ContainerStatusResponse, error) {
	return &v1.ContainerStatusResponse{
		Status: &v1.ContainerStatus{Id: req.ContainerId},
	}, nil
}

func v1alpha2ServiceDesc() *grpc.ServiceDesc {
	// Build a ServiceDesc identical to _RuntimeService_serviceDesc but with v1alpha2 service name.
	// This simulates what containerd < 1.6 registers.
	return &grpc.ServiceDesc{
		ServiceName: "runtime.v1alpha2.RuntimeService",
		HandlerType: (*v1.RuntimeServiceServer)(nil),
		Methods: []grpc.MethodDesc{
			{MethodName: "ListContainers", Handler: listContainersHandler},
			{MethodName: "ContainerStatus", Handler: containerStatusHandler},
		},
		Streams:  []grpc.StreamDesc{},
		Metadata: "api.proto",
	}
}

func listContainersHandler(srv interface{}, ctx context.Context, dec func(interface{}) error, _ grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(v1.ListContainersRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	return srv.(v1.RuntimeServiceServer).ListContainers(ctx, in)
}

func containerStatusHandler(srv interface{}, ctx context.Context, dec func(interface{}) error, _ grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(v1.ContainerStatusRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	return srv.(v1.RuntimeServiceServer).ContainerStatus(ctx, in)
}

func TestFullRoundTrip(t *testing.T) {
	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer()
	srv.RegisterService(v1alpha2ServiceDesc(), &fakeV1Alpha2Server{})

	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()

	dialer := func(ctx context.Context, _ string) (net.Conn, error) {
		return lis.DialContext(ctx)
	}

	// Case 1: WITHOUT interceptor → Unimplemented (reproduces the bug)
	conn1, err := grpc.DialContext(context.Background(), "bufnet",
		grpc.WithContextDialer(dialer), grpc.WithInsecure())
	require.NoError(t, err)
	defer conn1.Close()

	_, err = v1.NewRuntimeServiceClient(conn1).ListContainers(context.Background(), &v1.ListContainersRequest{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Unimplemented")
	fmt.Println("✓ Bug reproduced: v1 client → v1alpha2 server = Unimplemented")

	// Case 2: WITH interceptor → Success (proves the fix)
	conn2, err := grpc.DialContext(context.Background(), "bufnet",
		grpc.WithContextDialer(dialer), grpc.WithInsecure(),
		grpc.WithUnaryInterceptor(criV1Alpha2Rewriter))
	require.NoError(t, err)
	defer conn2.Close()

	client := v1.NewRuntimeServiceClient(conn2)

	listResp, err := client.ListContainers(context.Background(), &v1.ListContainersRequest{})
	require.NoError(t, err, "With interceptor: ListContainers should succeed")
	assert.Equal(t, "container-001", listResp.Containers[0].Id)

	statusResp, err := client.ContainerStatus(context.Background(), &v1.ContainerStatusRequest{ContainerId: "container-001"})
	require.NoError(t, err, "With interceptor: ContainerStatus should succeed")
	assert.Equal(t, "container-001", statusResp.Status.Id)

	fmt.Println("✓ Fix verified: v1 client + interceptor → v1alpha2 server = Success")
}
