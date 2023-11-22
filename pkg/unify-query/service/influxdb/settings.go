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
	"time"
)

const (
	TimeoutConfigPath              = "influxdb.timeout"
	PerQueryMaxGoroutineConfigPath = "influxdb.per_query_max_goroutine" // 单指标查询的最多并查询数
	ContentTypeConfigPath          = "influxdb.content_type"
	ChunkSizeConfigPath            = "influxdb.chunk_size"

	MaxLimitConfigPath  = "influxdb.max_limit"
	MaxSLimitConfigPath = "influxdb.max_slimit"
	ToleranceConfigPath = "influxdb.tolerance"

	PrefixConfigPath         = "influxdb.router.prefix"
	RouterIntervalConfigPath = "influxdb.router.interval"

	SpaceRouterPrefixConfigPath              = "influxdb.space_router_prefix"
	SpaceRouterBboltPathConfigPath           = "influxdb.space_router_bbolt_path"
	SpaceRouterBboltBucketNameConfigPath     = "influxdb.space_router_bbolt_bucket_name"
	SpaceRouterBboltWriteBatchSizeConfigPath = "influxdb.space_router_bbolt_write_batch_size"

	GrpcMaxCallRecvMsgSizeConfigPath = "influxdb.grpc_max_call_recv_msg_size"
	GrpcMaxCallSendMsgSizeConfigPath = "influxdb.grpc_max_call_send_msg_size"
)

var (
	Timeout              string
	PerQueryMaxGoroutine int
	ContentType          string
	ChunkSize            int

	MaxLimit  int
	MaxSLimit int
	Tolerance int

	RouterPrefix   string
	RouterInterval time.Duration

	SpaceRouterPrefix              string
	SpaceRouterBboltPath           string
	SpaceRouterBboltBucketName     string
	SpaceRouterBboltWriteBatchSize int

	GrpcMaxCallRecvMsgSize int
	GrpcMaxCallSendMsgSize int
)
