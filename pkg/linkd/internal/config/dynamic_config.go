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
	"regexp"
	"strings"
	"time"
)

const (
	// DynamicSourceAlarmLevel 从 Kingeye 的租户等级表读取配置。
	DynamicSourceAlarmLevel = "kingeye_alarmlevel"
	// DynamicSourceRedis 读取 Kingeye dynamicconfig v1 原始 JSON 字段。
	DynamicSourceRedis = "kingeye_dynamicconfig"
)

var dynamicTablePattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]{0,63}$`)

// DynamicConfigConfig 显式启用选定配置的同步；禁用时不校验或访问来源和快照存储。
type DynamicConfigConfig struct {
	Enabled  bool                           `yaml:"enabled" json:"enabled"`
	Sources  map[string]DynamicSourceConfig `yaml:"sources" json:"sources,omitempty"`
	Bindings DynamicBindings                `yaml:"bindings" json:"bindings"`
}

// DynamicBindings 枚举已实现的业务配置项，禁止任意覆盖启动配置。
type DynamicBindings struct {
	Severity *DynamicBinding `yaml:"severity,omitempty" json:"severity,omitempty"`
}

// DynamicBinding 将配置项绑定到一个来源；Key 仅用于 Redis 字段。
type DynamicBinding struct {
	Source string `yaml:"source" json:"source"`
	Key    string `yaml:"key,omitempty" json:"key,omitempty"`
}

// DynamicSourceConfig 定义独立来源连接，凭据不进入运行时快照。
type DynamicSourceConfig struct {
	Type                    string       `yaml:"type" json:"type"`
	MySQL                   *MySQLConfig `yaml:"mysql,omitempty" json:"-"`
	Redis                   *RedisConfig `yaml:"redis,omitempty" json:"-"`
	Table                   string       `yaml:"table,omitempty" json:"table,omitempty"`
	BKTenantID              string       `yaml:"bk_tenant_id" json:"bk_tenant_id"`
	RedisKeyPrefix          string       `yaml:"redis_key_prefix,omitempty" json:"redis_key_prefix,omitempty"`
	PollIntervalSeconds     int          `yaml:"poll_interval_seconds" json:"poll_interval_seconds"`
	OperationTimeoutSeconds int          `yaml:"operation_timeout_seconds" json:"operation_timeout_seconds"`
}

// WithDefaults 补齐来源参数；返回值不共享连接配置。
func (c DynamicSourceConfig) WithDefaults() DynamicSourceConfig {
	if c.BKTenantID == "" {
		c.BKTenantID = "system"
	}
	if c.Table == "" {
		c.Table = "alarm_alarmlevel"
	}
	if c.RedisKeyPrefix == "" {
		c.RedisKeyPrefix = "bk_monitor_base:"
	}
	if c.PollIntervalSeconds == 0 {
		c.PollIntervalSeconds = 30
		if c.Type == DynamicSourceRedis {
			c.PollIntervalSeconds = 300
		}
	}
	if c.OperationTimeoutSeconds == 0 {
		c.OperationTimeoutSeconds = 3
	}
	if c.MySQL != nil {
		v := *c.MySQL
		c.MySQL = &v
	}
	if c.Redis != nil {
		v := c.Redis.WithDefaults()
		c.Redis = &v
	}
	return c
}

// Timeout 限制一轮读取或快照存储操作。
func (c DynamicSourceConfig) Timeout() time.Duration {
	return time.Duration(c.WithDefaults().OperationTimeoutSeconds) * time.Second
}

// Interval 返回来源周期全量对账间隔。
func (c DynamicSourceConfig) Interval() time.Duration {
	return time.Duration(c.WithDefaults().PollIntervalSeconds) * time.Second
}

// Clone 隔离嵌套 map、绑定和连接参数。
func (c DynamicConfigConfig) Clone() DynamicConfigConfig {
	r := c
	r.Sources = make(map[string]DynamicSourceConfig, len(c.Sources))
	for k, v := range c.Sources {
		r.Sources[k] = v.WithDefaults()
	}
	if c.Bindings.Severity != nil {
		b := *c.Bindings.Severity
		r.Bindings.Severity = &b
	}
	return r
}

// Redacted 返回可展示的配置，不暴露 MySQL、Redis 或 Sentinel 密码。
func (c DynamicConfigConfig) Redacted() DynamicConfigConfig {
	r := c.Clone()
	for k, v := range r.Sources {
		if v.MySQL != nil && v.MySQL.Password != "" {
			v.MySQL.Password = redactedSecret
		}
		if v.Redis != nil {
			v.Redis = (StorageConfig{Redis: v.Redis}).Redacted().Redis
		}
		r.Sources[k] = v
	}
	return r
}

// Validate 只验证启用且被绑定的来源；保留的未绑定来源不构成启动依赖。
func (c DynamicConfigConfig) Validate() error {
	if !c.Enabled {
		return nil
	}
	b := c.Bindings.Severity
	if b == nil || b.Source == "" {
		return fmt.Errorf("bindings.severity.source is required")
	}
	s, ok := c.Sources[b.Source]
	if !ok {
		return fmt.Errorf("severity source is not configured")
	}
	s = s.WithDefaults()
	if err := validateBoundedText("bk_tenant_id", s.BKTenantID, 1, 256); err != nil {
		return err
	}
	if len(c.Sources) > 32 || len(b.Source) > 64 {
		return fmt.Errorf("dynamic config source capacity exceeded")
	}
	if s.PollIntervalSeconds < 1 || s.PollIntervalSeconds > 86400 || s.OperationTimeoutSeconds < 1 || s.OperationTimeoutSeconds > 60 {
		return fmt.Errorf("dynamic config interval or timeout out of bounds")
	}
	switch s.Type {
	case DynamicSourceAlarmLevel:
		if s.MySQL == nil || s.Redis != nil {
			return fmt.Errorf("alarmlevel requires only mysql connection")
		}
		if !dynamicTablePattern.MatchString(s.Table) {
			return fmt.Errorf("invalid alarmlevel table")
		}
		if b.Key != "" {
			return fmt.Errorf("alarmlevel does not use a Redis key")
		}
		return s.MySQL.Validate()
	case DynamicSourceRedis:
		if s.Redis == nil || s.MySQL != nil {
			return fmt.Errorf("dynamicconfig requires only redis connection")
		}
		if strings.ContainsAny(s.RedisKeyPrefix, "{}") || len(s.RedisKeyPrefix) > 256 {
			return fmt.Errorf("invalid dynamicconfig Redis prefix")
		}
		if err := validateBoundedText("binding.key", b.Key, 1, 256); err != nil {
			return err
		}
		return s.Redis.Validate()
	default:
		return fmt.Errorf("unknown dynamic config source type")
	}
}
