// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package consul

import (
	"fmt"

	consul "github.com/hashicorp/consul/api"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/config"
)

func NewConfig() *consul.Config {
	cfg := consul.DefaultConfig()

	port := 8500
	scheme := "http"
	if config.Configuration.Consul.HttpsPort != 0 {
		port = config.Configuration.Consul.HttpsPort
		scheme = "https"
	} else if config.Configuration.Consul.HttpPort != 0 {
		port = config.Configuration.Consul.HttpPort
		scheme = "http"
	} else {
		port = config.Configuration.Consul.Port
		scheme = config.Configuration.Consul.Scheme
	}

	cfg.Address = fmt.Sprintf("%s:%d", config.Configuration.Consul.Host, port)
	cfg.Scheme = scheme

	if cfg.HttpAuth == nil {
		cfg.HttpAuth = new(consul.HttpBasicAuth)
	}
	cfg.HttpAuth.Username = config.Configuration.Consul.Auth.User
	cfg.HttpAuth.Password = config.Configuration.Consul.Auth.Password
	cfg.Token = config.Configuration.Consul.Auth.ACLToken

	cfg.TLSConfig.Address = config.Configuration.Consul.TLS.Address
	cfg.TLSConfig.InsecureSkipVerify = config.Configuration.Consul.TLS.Verify
	cfg.TLSConfig.KeyFile = config.Configuration.Consul.TLS.KeyFile
	cfg.TLSConfig.CertFile = config.Configuration.Consul.TLS.CertFile
	cfg.TLSConfig.CAFile = config.Configuration.Consul.TLS.CAFile
	return cfg
}

func NewClient() (*consul.Client, error) {
	return consul.NewClient(NewConfig())
}
