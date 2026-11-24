// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package config

import "fmt"

// CloseControlConfig 只配置单次筛选关闭请求的内部批次并发。
type CloseControlConfig struct {
	// Concurrency 是单个请求同时处理的内部批次数，不限制不同 HTTP 请求的总并发。
	Concurrency int `yaml:"concurrency"`
}

// WithDefaults 默认单请求内部并发为 8。
func (c CloseControlConfig) WithDefaults() CloseControlConfig {
	if c.Concurrency == 0 {
		c.Concurrency = 8
	}
	return c
}

// Validate 校验请求内并发配置。
func (c CloseControlConfig) Validate() error {
	if c.WithDefaults().Concurrency < 1 {
		return fmt.Errorf("control_plane.close.concurrency must be positive")
	}
	return nil
}

// CloseControlSettings 返回不修改原配置的请求内并发。
func (c Config) CloseControlSettings() CloseControlConfig {
	if c.ControlPlane != nil && c.ControlPlane.Close != nil {
		return c.ControlPlane.Close.WithDefaults()
	}
	return (CloseControlConfig{}).WithDefaults()
}
