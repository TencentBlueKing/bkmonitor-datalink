// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package influxdb

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
)

// grpcTsDBType 是 InfluxDB gRPC 请求的 tsdb_type，与 TsDBRequestSecond 用的值相同。
var grpcTsDBType = metadata.InfluxDBStorageType + "_grpc"

// grpcRequestOutcome 给一次 gRPC 存储请求分类。gRPC 的失败带的是状态码，不是 HTTP 状态，按 HTTP 的分法对齐：
// 不可用、内部错误、限流（ResourceExhausted，对应 429）和鉴权失败（对应 401/403）是 error；
// 请求本身不成立的（参数、范围、前置条件、不存在）是 rejected；不是 gRPC 状态的错误按 FailureStatus 分。
func grpcRequestOutcome(err error) string {
	switch {
	case err == nil:
		return metric.TsDBOutcomeSuccess
	case errors.Is(err, context.Canceled):
		return metric.TsDBOutcomeCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return metric.TsDBOutcomeTimeout
	}
	st, ok := status.FromError(err)
	if !ok {
		return metric.TsDBRequestOutcome(err)
	}
	switch st.Code() {
	case codes.Canceled:
		return metric.TsDBOutcomeCanceled
	case codes.DeadlineExceeded:
		return metric.TsDBOutcomeTimeout
	case codes.InvalidArgument, codes.NotFound, codes.OutOfRange, codes.FailedPrecondition, codes.AlreadyExists:
		return metric.TsDBOutcomeRejected
	default:
		return metric.TsDBOutcomeError
	}
}
