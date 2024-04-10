// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package offlineDataArchive

import (
	"context"
	"time"

	"golang.org/x/time/rate"

	remoteRead "github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/service/influxdb/proto"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
)

var _ tsdb.Instance = (*Instance)(nil)

type Instance struct {
	Ctx           context.Context
	Address       string
	Timeout       time.Duration
	MaxLimit      int
	MaxSLimit     int
	Toleration    int
	ReadRateLimit float64

	GrpcMaxCallRecvMsgSize int
	GrpcMaxCallSendMsgSize int
}

type StreamSeriesSetOption struct {
	Span    *trace.Span
	Stream  remoteRead.QueryTimeSeriesService_RawClient
	Limiter *rate.Limiter
	Timeout time.Duration
}
