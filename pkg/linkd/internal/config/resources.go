// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import (
	"fmt"
	"strings"
)

const maxDynamicGroupTenants = 32

// ResourcesConfig 定义部署内共享的第三方资源和凭据；不进入 EventSource Release。
type ResourcesConfig struct {
	// CMDB 直连蓝鲸，只读服务实例及主机拓扑回源，不进入任何来源或策略发布。
	CMDB *CMDBResource `yaml:"cmdb,omitempty" json:"cmdb,omitempty"`
	// KingeyeDisplay 提供展示转换所需的 Redis 缓存。
	KingeyeDisplay *DisplayResource `yaml:"kingeye_display,omitempty" json:"kingeye_display,omitempty"`
	// DynamicGroup 将 Kingeye 无租户后缀的投影 keyspace 显式绑定到租户。
	DynamicGroup *DynamicGroupResource `yaml:"dynamic_group,omitempty" json:"dynamic_group,omitempty"`
	// MySQL 指向 Kingeye schema，供元数据 Reader 共用。
	MySQL *MySQLResource `yaml:"mysql,omitempty" json:"mysql,omitempty"`
	// OneModel 指向统一实例、关系和业务拓扑存储，独立于 Linkd Repository。
	OneModel *OneModelResource `yaml:"onemodel,omitempty" json:"onemodel,omitempty"`
}

// DisplayResource 只读取 Kingeye 已有展示缓存；KeyPrefix 与该环境缓存前缀一致。
type DisplayResource struct {
	Redis     RedisConfig `yaml:"redis" json:"redis"`
	KeyPrefix string      `yaml:"key_prefix,omitempty" json:"key_prefix,omitempty"`
}

// DynamicGroupResource 为每个租户配置其动态分组物化缓存所在的 Redis keyspace。
type DynamicGroupResource struct {
	Tenants map[string]DynamicGroupTenantResource `yaml:"tenants" json:"tenants"`
}

// DynamicGroupTenantResource 描述当前 Kingeye 写入端使用的 Redis 与 key 前缀。
type DynamicGroupTenantResource struct {
	Redis     RedisConfig `yaml:"redis" json:"redis"`
	KeyPrefix string      `yaml:"key_prefix" json:"key_prefix"`
}

// MySQLResource 定义公共资源使用的 MySQL 只读连接。
type MySQLResource struct {
	Address  string `yaml:"address" json:"address"`
	Database string `yaml:"database" json:"database"`
	Username string `yaml:"username" json:"username"`
	Password string `yaml:"password" json:"password"`
}

// OneModelResource 定义通用实例读取后端及可选的 CMDB 主线拓扑 ES 连接。
type OneModelResource struct {
	// Backend 只选择通用实例和关系边存储；CMDB 主线拓扑仍使用下方 ES 连接。
	Backend     string                 `yaml:"backend,omitempty" json:"backend,omitempty"`
	Doris       *OneModelDorisResource `yaml:"doris,omitempty" json:"doris,omitempty"`
	Addresses   []string               `yaml:"addresses" json:"addresses"`
	IndexPrefix string                 `yaml:"index_prefix,omitempty" json:"index_prefix,omitempty"`
	APIKey      string                 `yaml:"api_key,omitempty" json:"api_key,omitempty"`
	BasicAuth   *ResourceBasicAuth     `yaml:"basic_auth,omitempty" json:"basic_auth,omitempty"`
}

// ResourceBasicAuth 定义 OneModel Elasticsearch 的 Basic Auth 凭据。
type ResourceBasicAuth struct {
	Username string `yaml:"username" json:"username"`
	Password string `yaml:"password" json:"password"`
}

// Clone 返回不共享可变字段的资源配置副本。
func (c ResourcesConfig) Clone() ResourcesConfig {
	cloned := c
	if c.CMDB != nil {
		value := *c.CMDB
		cloned.CMDB = &value
	}
	if c.KingeyeDisplay != nil {
		value := *c.KingeyeDisplay
		value.Redis = value.Redis.clone()
		cloned.KingeyeDisplay = &value
	}
	if c.DynamicGroup != nil {
		value := DynamicGroupResource{Tenants: make(map[string]DynamicGroupTenantResource, len(c.DynamicGroup.Tenants))}
		for tenant, resource := range c.DynamicGroup.Tenants {
			resource.Redis = resource.Redis.clone()
			value.Tenants[tenant] = resource
		}
		cloned.DynamicGroup = &value
	}
	if c.MySQL != nil {
		value := *c.MySQL
		cloned.MySQL = &value
	}
	if c.OneModel != nil {
		value := *c.OneModel
		if c.OneModel.Doris != nil {
			d := *c.OneModel.Doris
			value.Doris = &d
		}
		value.Addresses = append([]string(nil), c.OneModel.Addresses...)
		if c.OneModel.BasicAuth != nil {
			basicAuth := *c.OneModel.BasicAuth
			value.BasicAuth = &basicAuth
		}
		cloned.OneModel = &value
	}
	return cloned
}

// Redacted 返回可公开展示的资源副本，隐藏所有认证凭据。
func (c ResourcesConfig) Redacted() ResourcesConfig {
	redacted := c.Clone()

	if redacted.KingeyeDisplay != nil {
		redacted.KingeyeDisplay.Redis = *(StorageConfig{Redis: &redacted.KingeyeDisplay.Redis}).Redacted().Redis
	}
	if redacted.DynamicGroup != nil {
		for tenant, resource := range redacted.DynamicGroup.Tenants {
			resource.Redis = *(StorageConfig{Redis: &resource.Redis}).Redacted().Redis
			redacted.DynamicGroup.Tenants[tenant] = resource
		}
	}
	if redacted.MySQL != nil && redacted.MySQL.Password != "" {
		redacted.MySQL.Password = redactedSecret
	}
	if redacted.OneModel != nil {
		if redacted.OneModel.Doris != nil && redacted.OneModel.Doris.Password != "" {
			redacted.OneModel.Doris.Password = redactedSecret
		}
		if redacted.OneModel.APIKey != "" {
			redacted.OneModel.APIKey = redactedSecret
		}
		if redacted.OneModel.BasicAuth != nil && redacted.OneModel.BasicAuth.Password != "" {
			redacted.OneModel.BasicAuth.Password = redactedSecret
		}
	}
	return redacted
}

func (c MySQLResource) validate() error {
	return MySQLConfig(c).Validate()
}

func (c OneModelResource) validate() error {
	switch c.Backend {
	case "", "elasticsearch":
		if c.Doris != nil {
			return fmt.Errorf("doris requires backend=doris")
		}
	case "doris":
		if c.Doris == nil {
			return fmt.Errorf("doris connection is required")
		}
		if err := c.Doris.Validate(); err != nil {
			return err
		}
		if len(c.Addresses) == 0 {
			if c.APIKey != "" || c.BasicAuth != nil {
				return fmt.Errorf("topology ES credentials require addresses")
			}
			return nil
		}
	default:
		return fmt.Errorf("unsupported onemodel backend")
	}

	var basicAuth *BasicAuthConfig
	if c.BasicAuth != nil {
		basicAuth = &BasicAuthConfig{Username: c.BasicAuth.Username, Password: c.BasicAuth.Password}
	}
	if c.IndexPrefix == "" {
		c.IndexPrefix = "bk_monitor_base_"
	}
	return (ElasticsearchConfig{
		Addresses: c.Addresses, IndexPrefix: c.IndexPrefix,
		APIKey: c.APIKey, BasicAuth: basicAuth,
	}).Validate()
}

// Validate 校验已声明的资源结构，不探测连接；未声明资源由使用方按实际依赖检查。
func (c ResourcesConfig) Validate() error {
	if c.CMDB != nil {
		if err := c.CMDB.Validate(); err != nil {
			return fmt.Errorf("resources.cmdb: %w", err)
		}
	}
	if c.MySQL != nil {
		if err := c.MySQL.validate(); err != nil {
			return fmt.Errorf("resources.mysql: %w", err)
		}
	}
	if c.OneModel != nil {
		if err := c.OneModel.validate(); err != nil {
			return fmt.Errorf("resources.onemodel: %w", err)
		}
	}
	if c.KingeyeDisplay != nil {
		if err := c.KingeyeDisplay.Redis.Validate(); err != nil {
			return fmt.Errorf("invalid resources.kingeye_display redis configuration")
		}
		if len(c.KingeyeDisplay.KeyPrefix) > 128 {
			return fmt.Errorf("resources.kingeye_display key prefix too long")
		}
	}
	if c.DynamicGroup != nil {
		if len(c.DynamicGroup.Tenants) == 0 || len(c.DynamicGroup.Tenants) > maxDynamicGroupTenants {
			return fmt.Errorf("resources.dynamic_group requires 1 to %d tenant caches", maxDynamicGroupTenants)
		}
		keyspaces := map[string]string{}
		for tenant, resource := range c.DynamicGroup.Tenants {
			if strings.TrimSpace(tenant) != tenant || tenant == "" || strings.ContainsAny(tenant, "\r\n") {
				return fmt.Errorf("resources.dynamic_group contains invalid tenant")
			}
			if resource.KeyPrefix == "" || len(resource.KeyPrefix) > 128 {
				return fmt.Errorf("resources.dynamic_group tenant %q requires a valid key_prefix", tenant)
			}
			if err := resource.Redis.Validate(); err != nil {
				return fmt.Errorf("resources.dynamic_group tenant %q has invalid redis configuration: %w", tenant, err)
			}
			connection := resource.Redis.WithDefaults()
			keyspace := fmt.Sprintf("%s|%s|%d|%s", connection.Mode, connection.Address, connection.Database, resource.KeyPrefix)
			if connection.Sentinel != nil {
				keyspace += "|" + connection.Sentinel.MasterName
			}
			if previous, exists := keyspaces[keyspace]; exists {
				return fmt.Errorf("resources.dynamic_group tenants %q and %q share a keyspace", previous, tenant)
			}
			keyspaces[keyspace] = tenant
		}
	}
	return nil
}
