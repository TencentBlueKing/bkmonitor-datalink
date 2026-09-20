// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 日志平台 (BlueKing - Log) available.
// Copyright (C) 2017-2021 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
//

//go:build windows
// +build windows

package controllers

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/Microsoft/go-winio"
	"github.com/containerd/containerd"
	"google.golang.org/grpc"
	v1 "k8s.io/cri-api/pkg/apis/runtime/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/define"
)

func criV1Alpha2Rewriter(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	method = strings.Replace(method, "/runtime.v1.RuntimeService/", "/runtime.v1alpha2.RuntimeService/", 1)
	return invoker(ctx, method, req, reply, cc, opts...)
}

func NewContainerdRuntime(useV1Alpha2 bool) (define.Runtime, error) {
	client, err := containerd.New(config.ContainerdAddress, containerd.WithDefaultNamespace(config.ContainerdNamespace))
	if err != nil {
		return nil, fmt.Errorf("create containerd client: %w", err)
	}

	timeout := time.Second * 10
	dialOpts := []grpc.DialOption{
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return winio.DialPipe(config.ContainerdAddress, &timeout)
		}),
		grpc.WithInsecure(),
	}
	if useV1Alpha2 {
		dialOpts = append(dialOpts, grpc.WithUnaryInterceptor(criV1Alpha2Rewriter))
	}
	conn, err := grpc.DialContext(context.Background(), "", dialOpts...)
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("dial containerd CRI: %w", err)
	}

	return &ContainerdRuntime{
		ContainerdBase: ContainerdBase{
			containerdClient: client,
			log:              ctrl.Log.WithName("containerd"),
		},
		cri: &criClient{client: v1.NewRuntimeServiceClient(conn)},
	}, nil
}
