// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package tsdb

import (
	"time"
)

type Storage struct {
	Type     string
	Address  string
	Username string
	Password string

	UriPath string
	Timeout time.Duration

	MaxLimit      int
	MaxSLimit     int
	Toleration    int
	ReadRateLimit float64

	ContentType string
	ChunkSize   int

	Accept         string
	AcceptEncoding string

	Instance Instance
}

type VMOption struct {
	UriPath string
	Timeout time.Duration
}

type InfluxDBOption struct {
	Timeout time.Duration

	ContentType string
	ChunkSize   int

	ReadRateLimit float64

	RawUriPath     string
	Accept         string
	AcceptEncoding string

	MaxLimit  int
	MaxSLimit int
	Tolerance int

	RouterPrefix string
}

type Options struct {
	VM       *VMOption
	InfluxDB *InfluxDBOption
}

type Host struct {
	Address string
}

type InstanceConfig struct {
	Type    string
	Address string
	Uri     string

	TimeOut time.Duration
}
