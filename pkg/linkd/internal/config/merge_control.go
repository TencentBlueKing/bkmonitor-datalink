// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package config

import "fmt"

// MergeControlConfig 控制自动合并的单批成员数和进程内并发，不限制一个合并组的总成员数。
type MergeControlConfig struct {
	MemberBatchSize int `yaml:"member_batch_size"`
	Concurrency     int `yaml:"concurrency"`
}

// WithDefaults 默认每批128成员、进程总并发8；有积压时连续分批推进。
func (c MergeControlConfig) WithDefaults() MergeControlConfig {
	if c.MemberBatchSize == 0 {
		c.MemberBatchSize = 128
	}
	if c.Concurrency == 0 {
		c.Concurrency = 8
	}
	return c
}

// Validate 校验部署配置，容量上界由已有存储字节预算和运行截止控制。
func (c MergeControlConfig) Validate() error {
	c = c.WithDefaults()
	if c.MemberBatchSize < 1 || c.Concurrency < 1 {
		return fmt.Errorf("control_plane.merge member_batch_size and concurrency must be positive")
	}
	return nil
}

// MergeControlSettings 返回省略配置时也可用于控制面运行的有效预算。
func (c Config) MergeControlSettings() MergeControlConfig {
	if c.ControlPlane != nil && c.ControlPlane.Merge != nil {
		return c.ControlPlane.Merge.WithDefaults()
	}
	return (MergeControlConfig{}).WithDefaults()
}
