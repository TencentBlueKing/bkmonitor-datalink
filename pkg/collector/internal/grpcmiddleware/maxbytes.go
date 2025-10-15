// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package grpcmiddleware

import (
	"google.golang.org/grpc"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/optmap"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	// Note: grpc 默认配置 调整此参数请评估影响）
	defaultMaxRequestBytes = 1024 * 1024 * 8 // 8MB
	optMaxRequestBytes     = "maxRequestBytes"
)

func init() {
	Register("maxbytes", MaxBytes)
}

func MaxBytes(opt string) grpc.ServerOption {
	om := optmap.New(opt)
	n := om.GetIntDefault(optMaxRequestBytes, defaultMaxRequestBytes)
	logger.Infof("maxbytes middleware opts: %s(%d)", optMaxRequestBytes, n)

	return grpc.MaxRecvMsgSize(n)
}
