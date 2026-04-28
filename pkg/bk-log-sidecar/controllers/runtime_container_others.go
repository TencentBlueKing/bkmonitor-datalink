// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 日志平台 (BlueKing - Log) available.
// Copyright (C) 2017-2021 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
//

//go:build !windows
// +build !windows

package controllers

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/containerd/containerd"
	"google.golang.org/grpc"
	v1 "k8s.io/cri-api/pkg/apis/runtime/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/utils"
)

// criV1Alpha2Rewriter rewrites gRPC method paths from runtime.v1.RuntimeService
// to runtime.v1alpha2.RuntimeService for containerd < 1.6 which only supports CRI v1alpha2.
// The protobuf wire format is identical between v1 and v1alpha2, only the service path differs.
func criV1Alpha2Rewriter(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	method = strings.Replace(method, "/runtime.v1.RuntimeService/", "/runtime.v1alpha2.RuntimeService/", 1)
	return invoker(ctx, method, req, reply, cc, opts...)
}

// NewContainerdRuntime creates a Runtime for containerd.
// When useV1Alpha2 is true, a gRPC interceptor rewrites CRI method paths to
// runtime.v1alpha2 for containerd < 1.6.
func NewContainerdRuntime(useV1Alpha2 bool) define.Runtime {
	client, err := containerd.New(config.ContainerdAddress, containerd.WithDefaultNamespace(config.ContainerdNamespace))
	utils.CheckError(err)

	ctx, _ := context.WithTimeout(context.Background(), 10*time.Second)
	dialOpts := []grpc.DialOption{grpc.WithInsecure()}
	if useV1Alpha2 {
		dialOpts = append(dialOpts, grpc.WithUnaryInterceptor(criV1Alpha2Rewriter))
	}
	conn, err := grpc.DialContext(ctx, fmt.Sprintf("unix://%s", config.ContainerdAddress), dialOpts...)
	utils.CheckError(err)

	return &ContainerdRuntime{
		ContainerdBase: ContainerdBase{
			containerdClient: client,
			log:              ctrl.Log.WithName("containerd"),
		},
		cri: &criClient{client: v1.NewRuntimeServiceClient(conn)},
	}
}
