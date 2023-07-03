// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package receiver

import (
	"github.com/elastic/beats/libbeat/common/transport/tlscommon"
)

type ComponentConfig struct {
	Jaeger      ComponentJaeger      `config:"jaeger"`
	Otlp        ComponentOtlp        `config:"otlp"`
	PushGateway ComponentPushGateway `config:"pushgateway"`
	RemoteWrite ComponentRemoteWrite `config:"remotewrite"`
	Zipkin      ComponentZipkin      `config:"zipkin"`
	Skywalking  ComponentSkywalking  `config:"skywalking"`
}

type ComponentJaeger struct {
	Enabled bool `config:"enabled"`
}

type ComponentOtlp struct {
	Enabled bool `config:"enabled"`
}

type ComponentPushGateway struct {
	Enabled bool `config:"enabled"`
}

type ComponentRemoteWrite struct {
	Enabled bool `config:"enabled"`
}

type ComponentZipkin struct {
	Enabled bool `config:"enabled"`
}

type ComponentSkywalking struct {
	Enabled bool `config:"enabled"`
}

type Config struct {
	HttpServer HttpServerConfig `config:"http_server"`
	GrpcServer GrpcServerConfig `config:"grpc_server"`
	Components ComponentConfig  `config:"components"`
}

type HttpServerConfig struct {
	Enabled     bool                    `config:"enabled"`
	Endpoint    string                  `config:"endpoint"`
	Middlewares []string                `config:"middlewares"`
	TLS         *tlscommon.ServerConfig `config:"ssl"`
}

type GrpcServerConfig struct {
	Enabled     bool     `config:"enabled"`
	Endpoint    string   `config:"endpoint"`
	Middlewares []string `config:"middlewares"`
	Transport   string   `config:"transport"`
}
