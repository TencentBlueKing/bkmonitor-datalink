// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import "fmt"

// ResourcesConfig 定义部署内共享的第三方只读资源；不进入 EventSource Release。
type ResourcesConfig struct {
	// KingeyeDisplay 提供展示转换所需的 Redis 缓存。
	KingeyeDisplay *DisplayResource `yaml:"kingeye_display,omitempty" json:"kingeye_display,omitempty"`
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

// MySQLResource 定义公共资源使用的 MySQL 只读连接。
type MySQLResource struct {
	Address  string `yaml:"address" json:"address"`
	Database string `yaml:"database" json:"database"`
	Username string `yaml:"username" json:"username"`
	Password string `yaml:"password" json:"password"`
}

// OneModelResource 定义公共资源使用的 Elasticsearch 只读连接。
type OneModelResource struct {
	Addresses   []string           `yaml:"addresses" json:"addresses"`
	IndexPrefix string             `yaml:"index_prefix,omitempty" json:"index_prefix,omitempty"`
	APIKey      string             `yaml:"api_key,omitempty" json:"api_key,omitempty"`
	BasicAuth   *ResourceBasicAuth `yaml:"basic_auth,omitempty" json:"basic_auth,omitempty"`
}

// ResourceBasicAuth 定义 OneModel Elasticsearch 的 Basic Auth 凭据。
type ResourceBasicAuth struct {
	Username string `yaml:"username" json:"username"`
	Password string `yaml:"password" json:"password"`
}

// Clone 返回不共享可变字段的资源配置副本。
func (c ResourcesConfig) Clone() ResourcesConfig {
	cloned := c
	if c.KingeyeDisplay != nil {
		value := *c.KingeyeDisplay
		value.Redis = value.Redis.clone()
		cloned.KingeyeDisplay = &value
	}
	if c.MySQL != nil {
		value := *c.MySQL
		cloned.MySQL = &value
	}
	if c.OneModel != nil {
		value := *c.OneModel
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
	if redacted.MySQL != nil && redacted.MySQL.Password != "" {
		redacted.MySQL.Password = redactedSecret
	}
	if redacted.OneModel != nil {
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
	return nil
}
