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
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// PluginsConfig 保存部署级内置插件；不随 EventSource 发布，也不改变租户隔离。
type PluginsConfig struct {
	// KAC 是全来源和全租户共用的兼容存储与处置插件。
	KAC *KACPluginConfig `yaml:"kac,omitempty" json:"kac,omitempty"`
}

// KACPluginConfig 同时启用直接维护 alarm_event 和可靠处置通知，覆盖全部来源及租户。
type KACPluginConfig struct {
	// Enabled 一并启用兼容存储与处置通知，不提供逐租户或逐来源开关。
	Enabled bool `yaml:"enabled" json:"enabled"`
	// Elasticsearch 指向原 KAC 集群，与 Linkd Repository 的连接相互独立。
	Elasticsearch KACElasticsearchConfig `yaml:"elasticsearch" json:"elasticsearch"`
	// AlarmEventIndex 是 KAC 原查询/写入 alias，不能使用 Linkd Repository 的索引前缀替代。
	AlarmEventIndex string `yaml:"alarm_event_index" json:"alarm_event_index"`
	// ActionEndpoint 必须实现动作 V2 的持久幂等受理，不得指向旧告警 pipeline。
	ActionEndpoint string `yaml:"action_endpoint" json:"action_endpoint"`
	// JWT 保存 KAC 的签名密钥与调用身份；每次投递重新签发，不保存已签发 Token。
	JWT JWTConfig `yaml:"jwt" json:"jwt"`
}

// KACElasticsearchConfig 保存兼容索引连接和与 KAC 对齐的新建索引参数。
// 参数只影响新建索引；已存在物理索引的分片、副本及运维设置不被周期维护重置。
type KACElasticsearchConfig struct {
	// Addresses 原 KAC ES 节点列表，所有调用不跟随重定向。
	Addresses []string `yaml:"addresses" json:"addresses"`
	// APIKey 可选 API Key，与 BasicAuth 互斥。
	APIKey string `yaml:"api_key,omitempty" json:"api_key,omitempty"`
	// BasicAuth 可选 Basic Auth，只在部署配置中保存。
	BasicAuth *ResourceBasicAuth `yaml:"basic_auth,omitempty" json:"basic_auth,omitempty"`
	// NumberOfShards 首次创建模板时的主分片数，缺省 3。
	NumberOfShards int `yaml:"number_of_shards,omitempty" json:"number_of_shards,omitempty"`
	// NumberOfReplicas 首次创建模板及元数据索引的副本数，nil 为 2，允许显式 0。
	NumberOfReplicas *int `yaml:"number_of_replicas,omitempty" json:"number_of_replicas,omitempty"`
	// MaxResultWindow 首次创建模板时的查询窗口上限，缺省 50000。
	MaxResultWindow int `yaml:"max_result_window,omitempty" json:"max_result_window,omitempty"`
	// TotalFieldsLimit 首次创建模板时的字段数量上限，缺省 5000。
	TotalFieldsLimit int `yaml:"total_fields_limit,omitempty" json:"total_fields_limit,omitempty"`
}

var kacIndexName = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,179}$`)

// KACEnabled 表示整套兼容存储与处置通知已启用，没有来源或租户二级开关。
func (c PluginsConfig) KACEnabled() bool { return c.KAC != nil && c.KAC.Enabled }

// WithDefaults 使用 KAC 源码的索引默认参数；连接和 alias 必须由部署显式提供。
func (c KACPluginConfig) WithDefaults() KACPluginConfig {
	if c.JWT.Username == "" {
		c.JWT.Username = "admin"
	}
	c.Elasticsearch.Addresses = append([]string(nil), c.Elasticsearch.Addresses...)
	if c.Elasticsearch.BasicAuth != nil {
		a := *c.Elasticsearch.BasicAuth
		c.Elasticsearch.BasicAuth = &a
	}
	if c.Elasticsearch.NumberOfShards == 0 {
		c.Elasticsearch.NumberOfShards = 3
	}
	n := 2
	if c.Elasticsearch.NumberOfReplicas != nil {
		n = *c.Elasticsearch.NumberOfReplicas
	}
	c.Elasticsearch.NumberOfReplicas = &n
	if c.Elasticsearch.MaxResultWindow == 0 {
		c.Elasticsearch.MaxResultWindow = 50000
	}
	if c.Elasticsearch.TotalFieldsLimit == 0 {
		c.Elasticsearch.TotalFieldsLimit = 5000
	}
	return c
}

// Validate 拒绝不完整或不安全的全局出口，不回显地址、认证或 Token。
func (c PluginsConfig) Validate() error {
	if c.KAC == nil {
		return nil
	}
	v := c.KAC.WithDefaults()
	if !v.Enabled {
		return nil
	}
	if !kacIndexName.MatchString(v.AlarmEventIndex) || strings.Contains(v.AlarmEventIndex, "..") {
		return fmt.Errorf("plugins.kac.alarm_event_index is invalid")
	}
	es := v.Elasticsearch
	if err := (OneModelResource{Addresses: es.Addresses, APIKey: es.APIKey, BasicAuth: es.BasicAuth}).validate(); err != nil {
		return fmt.Errorf("plugins.kac.elasticsearch connection is invalid")
	}
	if es.NumberOfShards < 1 || es.NumberOfShards > 1024 || *es.NumberOfReplicas < 0 || *es.NumberOfReplicas > 10 || es.MaxResultWindow < 1 || es.MaxResultWindow > 1000000 || es.TotalFieldsLimit < 1 || es.TotalFieldsLimit > 100000 {
		return fmt.Errorf("plugins.kac.elasticsearch index settings are invalid")
	}
	if _, err := kacEndpointOrigin(v.ActionEndpoint, false); err != nil {
		return fmt.Errorf("plugins.kac.action_endpoint is invalid")
	}
	if strings.TrimSpace(v.JWT.SecretKey) == "" || len(v.JWT.SecretKey) > 16<<10 || v.JWT.SecretKey == redactedSecret {
		return fmt.Errorf("plugins.kac.jwt.secret_key is invalid")
	}
	if strings.TrimSpace(v.JWT.Username) == "" || len(v.JWT.Username) > 256 {
		return fmt.Errorf("plugins.kac.jwt.username is invalid")
	}
	return nil
}

// Redacted 返回独立副本，公共凭据不进入 Console 或 config show 输出。
func (c PluginsConfig) Redacted() PluginsConfig {
	if c.KAC == nil {
		return c
	}
	v := c.KAC.WithDefaults()
	if v.JWT.SecretKey != "" {
		v.JWT.SecretKey = redactedSecret
	}
	if v.Elasticsearch.APIKey != "" {
		v.Elasticsearch.APIKey = redactedSecret
	}
	if v.Elasticsearch.BasicAuth != nil && v.Elasticsearch.BasicAuth.Password != "" {
		v.Elasticsearch.BasicAuth.Password = redactedSecret
	}
	return PluginsConfig{KAC: &v}
}

func kacEndpointOrigin(raw string, onlyOrigin bool) (string, error) {
	if raw == "" || len(raw) > 2048 || !utf8.ValidString(raw) || strings.ContainsAny(raw, "\\#") {
		return "", fmt.Errorf("invalid KAC endpoint")
	}
	for _, r := range raw {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return "", fmt.Errorf("invalid KAC endpoint")
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || strings.Contains(u.Hostname(), "*") || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return "", fmt.Errorf("invalid KAC endpoint")
	}
	if onlyOrigin && u.Path != "" && u.Path != "/" {
		return "", fmt.Errorf("invalid KAC origin")
	}
	port := u.Port()
	if port == "" {
		port = "80"
		if u.Scheme == "https" {
			port = "443"
		}
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return "", fmt.Errorf("invalid KAC port")
	}
	return u.Scheme + "://" + net.JoinHostPort(strings.ToLower(u.Hostname()), strconv.Itoa(n)), nil
}
