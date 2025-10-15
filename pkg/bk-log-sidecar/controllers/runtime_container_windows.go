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
	"net"
	"time"

	"github.com/Microsoft/go-winio"
	"github.com/containerd/containerd"
	"google.golang.org/grpc"
	"k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/utils"
)

func NewContainerdRuntime() define.Runtime {
	client, err := containerd.New(config.ContainerdAddress, containerd.WithDefaultNamespace(config.ContainerdNamespace))
	utils.CheckError(err)

	timeout := time.Second * 10
	dialOpt := grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
		return winio.DialPipe(config.ContainerdAddress, &timeout)
	})
	conn, err := grpc.DialContext(context.Background(), "", dialOpt, grpc.WithInsecure())
	utils.CheckError(err)

	return &ContainerdRuntime{
		containerdClient: client,
		log:              ctrl.Log.WithName("containerd"),
		criClient:        v1alpha2.NewRuntimeServiceClient(conn),
	}
}
