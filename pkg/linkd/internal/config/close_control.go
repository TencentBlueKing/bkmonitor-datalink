// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package config

import (
	"fmt"

	"linkd/internal/store"
)

// CloseControlConfig 是筛选关闭独立的批次和成员预算，不占用单条关闭或 Lifecycle Worker 的名额。
type CloseControlConfig struct {
	// BatchSize 是一次内部读写批次的大小，与 HTTP 候选 limit 独立。
	BatchSize int `yaml:"batch_size"`
	// Concurrency 是控制面全部筛选关闭请求共用的在途内部批次数，不占用单条关闭名额。
	Concurrency int `yaml:"concurrency"`
	// MemberConcurrency 是每来源子批的前置调用、清理及 Hook 并发。
	MemberConcurrency int `yaml:"member_concurrency"`
}

// WithDefaults 默认每批 1000 条、最多 8 个内部批次、每批 32 个成员外部调用并发。
func (c CloseControlConfig) WithDefaults() CloseControlConfig {
	if c.BatchSize == 0 {
		c.BatchSize = 1000
	}
	if c.Concurrency == 0 {
		c.Concurrency = 8
	}
	if c.MemberConcurrency == 0 {
		c.MemberConcurrency = 32
	}
	return c
}

// Validate 校验关闭 HTTP 请求预算；ES 请求仍按现有字节预算切片。
func (c CloseControlConfig) Validate() error {
	c = c.WithDefaults()
	if c.BatchSize < 1 || c.BatchSize > store.MaxCloseBatchSize || c.Concurrency < 1 || c.MemberConcurrency < 1 {
		return fmt.Errorf("control_plane.close requires batch_size in 1..%d and positive concurrency/member_concurrency", store.MaxCloseBatchSize)
	}
	return nil
}

// CloseControlSettings 返回不修改原配置的筛选关闭预算。
func (c Config) CloseControlSettings() CloseControlConfig {
	if c.ControlPlane != nil && c.ControlPlane.Close != nil {
		return c.ControlPlane.Close.WithDefaults()
	}
	return (CloseControlConfig{}).WithDefaults()
}
