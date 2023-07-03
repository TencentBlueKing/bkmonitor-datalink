// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import (
	"strconv"
	"strings"

	consul "github.com/hashicorp/consul/api"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/utils"
)

type authConfig struct {
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	ACLToken string `yaml:"acl_token"`
}

type tlsConfig struct {
	Address  string `yaml:"address"`
	Verify   bool   `yaml:"verify"`
	CAFile   string `yaml:"ca_file"`
	KeyFile  string `yaml:"key_file"`
	CertFile string `yaml:"cert_file"`
}

type Consul struct {
	Scheme          string     `yaml:"scheme"`
	Host            string     `yaml:"host"`
	Port            int        `yaml:"port"`
	HttpPort        int        `yaml:"http_port"`
	HttpsPort       int        `yaml:"https_port"`
	EventBufferSize int        `yaml:"event_buffer_size"`
	DataIDPath      string     `yaml:"data_id_path"`
	ServicePath     string     `yaml:"service_path"`
	Auth            authConfig `yaml:"auth"`
	TLS             tlsConfig  `yaml:"tls"`
	ServiceName     string     `yaml:"service_name"`
	ServiceTag      string     `yaml:"service_tag"`
	ClientTTL       string     `yaml:"client_ttl"`
}

func (c *Consul) Init() {
	cfg := consul.DefaultConfig()

	c.Scheme = cfg.Scheme
	c.Host = "127.0.0.1"
	c.Port = 8500

	c.EventBufferSize = 32
	c.DataIDPath = "bk_bkmonitorv3_enterprise_production/metadata/v1"
	c.ServicePath = "bk_bkmonitorv3_enterprise_production/ingester"

	if cfg.HttpAuth != nil {
		c.Auth.User = cfg.HttpAuth.Username
		c.Auth.Password = cfg.HttpAuth.Password
	}

	c.Auth.ACLToken = cfg.Token

	c.TLS.Address = cfg.TLSConfig.Address
	c.TLS.Verify = cfg.TLSConfig.InsecureSkipVerify
	c.TLS.CAFile = cfg.TLSConfig.CAFile
	c.TLS.KeyFile = cfg.TLSConfig.KeyFile
	c.TLS.CertFile = cfg.TLSConfig.CertFile

	c.ServiceName = "bkmonitorv3"
	c.ServiceTag = "ingester"

	c.ClientTTL = "30s"
}

func (c *Consul) GetDataIDPathPrefix() string {
	dataIDPath := c.DataIDPath
	if !strings.HasSuffix(dataIDPath, "/") {
		dataIDPath = dataIDPath + "/"
	}
	return dataIDPath
}

func (c *Consul) GetShadowPathPrefix() string {
	shadowPath := utils.ResolveUnixPath(c.ServicePath, "data_id")
	if !strings.HasSuffix(shadowPath, "/") {
		shadowPath = shadowPath + "/"
	}
	return shadowPath
}

func (c *Consul) GetShadowPath(service string, dataID int) string {
	return utils.ResolveUnixPaths(c.ServicePath, "data_id", service, strconv.Itoa(dataID))
}

func (c *Consul) GetDataIDContextPath(dataID int) string {
	return utils.ResolveUnixPaths(c.ServicePath, "context", strconv.Itoa(dataID))
}
