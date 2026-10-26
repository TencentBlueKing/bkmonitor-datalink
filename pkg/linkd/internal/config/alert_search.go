// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package config

import "fmt"

// AlertSearchConfig 定义通用告警检索的进程级预算；零值采用与现有管理读取相同的默认预算。
type AlertSearchConfig struct {
	// MaxConcurrent 是每个控制面进程同时执行的检索数，满时直接返回 429。
	MaxConcurrent int `yaml:"max_concurrent" json:"max_concurrent"`
	// TimeoutSeconds 受管理 API 的十秒总超时约束。
	TimeoutSeconds int `yaml:"timeout_seconds" json:"timeout_seconds"`
	// MaxRequestBytes 限制包含 DSL 的 HTTP 请求正文。
	MaxRequestBytes int64 `yaml:"max_request_bytes" json:"max_request_bytes"`
	// MaxResponseBytes 限制编码后的完整告警结果页。
	MaxResponseBytes int `yaml:"max_response_bytes" json:"max_response_bytes"`
}

// WithDefaults 沿用管理查询的两个并发槽、五秒超时、1 MiB 请求与 8 MiB 响应预算。
func (c AlertSearchConfig) WithDefaults() AlertSearchConfig {
	if c.MaxConcurrent == 0 {
		c.MaxConcurrent = 2
	}
	if c.TimeoutSeconds == 0 {
		c.TimeoutSeconds = 5
	}
	if c.MaxRequestBytes == 0 {
		c.MaxRequestBytes = 1 << 20
	}
	if c.MaxResponseBytes == 0 {
		c.MaxResponseBytes = 8 << 20
	}
	return c
}

// Validate 拒绝无法执行的预算和超过管理 API 总超时的配置。
func (c AlertSearchConfig) Validate() error {
	c = c.WithDefaults()
	if c.MaxConcurrent < 1 || c.MaxRequestBytes < 1 || c.MaxResponseBytes < 1 || c.TimeoutSeconds < 1 || c.TimeoutSeconds > 10 {
		return fmt.Errorf("dispatch.alert_search requires positive budgets and timeout_seconds between 1 and 10")
	}
	return nil
}
