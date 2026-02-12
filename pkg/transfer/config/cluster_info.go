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
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// SimpleMetaClusterInfo :
type SimpleMetaClusterInfo struct {
	*MetaClusterInfo
	ClusterConfigHelper *utils.MapHelper
	StorageConfigHelper *utils.MapHelper
	AuthInfoHelper      *utils.MapHelper
}

// NewSimpleMetaClusterInfo :
func NewSimpleMetaClusterInfo(conf *MetaClusterInfo) *SimpleMetaClusterInfo {
	return &SimpleMetaClusterInfo{
		MetaClusterInfo:     conf,
		ClusterConfigHelper: utils.NewMapHelper(conf.ClusterConfig),
		StorageConfigHelper: utils.NewMapHelper(conf.StorageConfig),
		AuthInfoHelper:      utils.NewMapHelper(conf.AuthInfo),
	}
}

// SetDomain :
func (c *SimpleMetaClusterInfo) SetSchema(schema string) {
	c.ClusterConfig["schema"] = schema
}

// GetSchema :
func (c *SimpleMetaClusterInfo) GetSchema() string {
	switch val := c.MustGetClusterConfig("schema").(type) {
	case string:
		return val
	case nil:
		return "http"
	default:
		panic(define.ErrType)
	}
}

// GetDomain :
func (c *SimpleMetaClusterInfo) GetDomain() string {
	return c.ClusterConfigHelper.MustGetString("domain_name")
}

// SetDomain :
func (c *SimpleMetaClusterInfo) SetDomain(domain string) {
	c.ClusterConfigHelper.Set("domain_name", domain)
}

// GetPort :
func (c *SimpleMetaClusterInfo) GetPort() int {
	return c.ClusterConfigHelper.MustGetInt("port")
}

// SetPort :
func (c *SimpleMetaClusterInfo) SetPort(port int) {
	c.ClusterConfigHelper.Set("port", port)
}

// GetAddress :
func (c *SimpleMetaClusterInfo) GetAddress() string {
	schema := c.GetSchema()
	if schema == "" {
		schema = "http"
	}
	return fmt.Sprintf("%s://%s:%d", schema, c.GetDomain(), c.GetPort())
}

// GetSSLInsecureSkipVerify 获取是否跳过 SSL 证书校验
// 默认为 true，即跳过校验
func (c *SimpleMetaClusterInfo) GetSSLInsecureSkipVerify() bool {
	if skipVerify, ok := c.ClusterConfigHelper.GetBool("ssl_insecure_skip_verify"); ok {
		logging.Debugf("[DEBUG] GetSSLInsecureSkipVerify from config: %v, cluster_config: %+v", skipVerify, c.ClusterConfig)
		return skipVerify
	}
	logging.Debugf("[DEBUG] GetSSLInsecureSkipVerify using default true, cluster_config: %+v", c.ClusterConfig)
	return true
}
