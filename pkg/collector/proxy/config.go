// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package proxy

import (
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
)

const (
	defaultConsulSrvName = "bkmonitorv3"
	defaultConsulSrvTag  = "report"
	defaultConsulAddr    = "http://127.0.0.1:8500"
	defaultConsulTTL     = "30s"
)

type HttpConfig struct {
	Host        string   `config:"host"`
	Port        int      `config:"port"`
	RetryListen bool     `config:"retry_listen"`
	Middlewares []string `config:"middlewares"`
}

func (h HttpConfig) Address() string {
	return fmt.Sprintf("%s:%d", h.Host, h.Port)
}

type ConsulConfig struct {
	Enabled bool   `config:"enabled"`
	SrvName string `config:"service_name"`
	SrvTag  string `config:"service_tag"`
	Addr    string `config:"address"`
	TTL     string `config:"ttl"`
}

func (cc ConsulConfig) Get() ConsulConfig {
	if cc.SrvName == "" {
		cc.SrvName = defaultConsulSrvName
	}
	if cc.SrvTag == "" {
		cc.SrvTag = defaultConsulSrvTag
	}
	if cc.Addr == "" {
		cc.Addr = defaultConsulAddr
	}
	if cc.TTL == "" {
		cc.TTL = defaultConsulTTL
	}

	return cc
}

type Config struct {
	Disabled bool         `config:"disabled"`
	Http     HttpConfig   `config:"http"`
	Consul   ConsulConfig `config:"consul"`
}

func LoadConfig(conf *confengine.Config) (*Config, error) {
	config := &Config{}
	if err := conf.UnpackChild(define.ConfigFieldProxy, config); err != nil {
		return nil, err
	}

	return config, nil
}
