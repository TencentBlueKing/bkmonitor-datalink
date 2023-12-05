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
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/eventbus"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

const (
	ConfKeySchema               = "consul.schema"
	ConfKeyHost                 = "consul.host"
	ConfKeyPort                 = "consul.port"
	ConfKeyHTTPPort             = "consul.http_port"
	ConfKeyHTTPSPort            = "consul.https_port"
	ConfKeyClientTTL            = "consul.client_ttl"
	ConfKeyClientID             = "consul.client_id"
	ConfKeyServiceName          = "consul.service_name"
	ConfKeyServiceTag           = "consul.service_tag"
	ConfKeyServicePath          = "consul.service_path"
	ConfKeyDataIDPath           = "consul.data_id_path"
	ConfKeyManualPath           = "consul.manual_path"
	ConfKeySamplingDataSubPath  = "consul.sampling_subpath"
	ConfKeyCheckInterval        = "consul.check_interval"
	ConfKeyDispatchDelay        = "consul.dispatch.delay"
	ConfKeyDispatchInterval     = "consul.dispatch.interval"
	ConfKeyDebugDispatcher      = "consul.debug_dispatcher"
	ConfKeySamplingInterval     = "consul.sampling_interval"
	ConfKeyEventBufferSize      = "consul.event_buffer_size"
	ConfKeyDispatchRetries      = "consul.dispatch.retries"
	ConfKeyShadowCopyBufferSize = "consul.shadow_buffer_size"
	ConfKeyTLSServer            = "consul.tls.server"
	ConfKeyTLSCAFile            = "consul.tls.ca_file"
	ConfKeyTLSCertFile          = "consul.tls.cert_file"
	ConfKeyTLSKeyFile           = "consul.tls.key_file"
	ConfKeyTLSVerify            = "consul.tls.verify"
	ConfKeyAuthBasicUser        = "consul.auth.basic.user"
	ConfKeyAuthBasicPassword    = "consul.auth.basic.password"
	ConfKeyAuthACLToken         = "consul.auth.acl_token"

	ConfServiceNameKey = "name"

	ConfKeyClusterID   = "cluster"
	ConfKeyPathVersion = "consul.path_version"
)

func initConfiguration(c define.Configuration) {
	cfg := NewDefaultConsulConfig()

	c.SetDefault(ConfKeySchema, cfg.Scheme)
	c.SetDefault(ConfKeyHost, "127.0.0.1")
	c.Set(ConfKeyPort, 8500)
	c.SetDefault(ConfKeySamplingInterval, time.Minute)
	// 此处先配置了一个基础的默认值，但是在cmd/root.go中会对默认值增加上版本及集群的配置信息
	c.SetDefault(ConfKeyDataIDPath, "bk_bkmonitorv3_enterprise_production/metadata")
	c.SetDefault(ConfKeyManualPath, "bk_bkmonitorv3_enterprise_production/manual")
	c.SetDefault(ConfKeyServicePath, "bk_bkmonitorv3_enterprise_production/service")
	c.SetDefault(ConfKeySamplingDataSubPath, "result_table")
	c.SetDefault(ConfKeyCheckInterval, "10s")
	c.SetDefault(ConfKeyClientTTL, "30s")
	c.SetDefault(ConfKeyClientID, define.ProcessID)
	c.SetDefault(ConfKeyServiceName, "bkmonitor")
	c.SetDefault(ConfKeyServiceTag, "transfer")
	c.SetDefault(ConfKeyEventBufferSize, 32)
	c.SetDefault(ConfKeyDispatchRetries, 3)
	c.SetDefault(ConfKeyShadowCopyBufferSize, 0)
	c.SetDefault(ConfKeyDispatchDelay, "3s")
	c.SetDefault(ConfKeyDispatchInterval, "10m")
	c.SetDefault(ConfKeyAuthBasicUser, cfg.HttpAuth.Username)
	c.SetDefault(ConfKeyAuthBasicPassword, cfg.HttpAuth.Password)
	c.SetDefault(ConfKeyAuthACLToken, cfg.Token)
	c.SetDefault(ConfKeyTLSServer, cfg.TLSConfig.Address)
	c.SetDefault(ConfKeyTLSCAFile, cfg.TLSConfig.CAFile)
	c.SetDefault(ConfKeyTLSCertFile, cfg.TLSConfig.CertFile)
	c.SetDefault(ConfKeyTLSKeyFile, cfg.TLSConfig.KeyFile)
	c.SetDefault(ConfKeyTLSVerify, cfg.TLSConfig.InsecureSkipVerify)
	c.SetDefault(ConfKeyDebugDispatcher, false)

	c.SetDefault(ConfKeyClusterID, "default")
	c.SetDefault(ConfKeyPathVersion, "v1")
}

func readConfiguration(c define.Configuration) {
	if c.IsSet(ConfKeyHTTPSPort) {
		c.RegisterAlias(ConfKeyPort, ConfKeyHTTPSPort)
		c.SetDefault(ConfKeySchema, "https")
	} else if c.IsSet(ConfKeyHTTPPort) {
		c.RegisterAlias(ConfKeyPort, ConfKeyHTTPPort)
		c.SetDefault(ConfKeySchema, "http")
	}
}

func init() {
	utils.CheckError(eventbus.Subscribe(eventbus.EvSysConfigPreParse, initConfiguration))
	utils.CheckError(eventbus.Subscribe(eventbus.EvSysConfigPostParse, readConfiguration))
}
