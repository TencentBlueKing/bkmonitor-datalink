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
	"time"

	"github.com/influxdata/influxdb/prometheus/remote"
	"golang.org/x/time/rate"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/curl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
)

// Instance influxDB 查询引擎
type Instance struct {
	tsdb.DefaultInstance

	ctx context.Context

	host     string
	port     int
	grpcPort int

	username string
	password string

	protocol      string
	readRateLimit float64

	timeout time.Duration
	curl    curl.Curl

	contentType string
	chunkSize   int

	rawUriPath     string
	accept         string
	acceptEncoding string

	maxLimit  int
	maxSLimit int
	tolerance int
}

type Options struct {
	Host     string
	Port     int
	GrpcPort int
	Username string
	Password string
	Timeout  time.Duration

	Protocol      string
	ReadRateLimit float64

	ContentType string
	ChunkSize   int

	RawUriPath     string
	Accept         string
	AcceptEncoding string

	MaxLimit  int
	MaxSlimit int
	Tolerance int

	Curl curl.Curl
}

type StreamSeriesSetOption struct {
	Span       *trace.Span
	Stream     remote.QueryTimeSeriesService_RawClient
	Limiter    *rate.Limiter
	Timeout    time.Duration
	MetricName string
}
