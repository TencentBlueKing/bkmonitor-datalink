// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package enrich

import (
	"context"
	"errors"
	"fmt"
	"math"
)

// ErrInjectedTestFailure 标记模拟数据源主动注入的调用失败，不包含业务数据。
var ErrInjectedTestFailure = errors.New("injected test datasource failure")

// TestSourceConfig 是测试处理器调用模拟数据源的有界负载参数。
// 延迟按正态分布采样后裁剪至 [0, SleepMaxMilliseconds]，实际均值可能因裁剪偏移。
type TestSourceConfig struct {
	// Calls 是单次丰富最多顺序调用的次数，首次错误后停止，范围 1..16。
	Calls int `json:"calls"`
	// SleepMeanMilliseconds 是裁剪前正态分布的期望，单位毫秒。
	SleepMeanMilliseconds float64 `json:"sleep_mean_milliseconds"`
	// SleepStddevMilliseconds 是裁剪前标准差；0 表示固定等待。
	SleepStddevMilliseconds float64 `json:"sleep_stddev_milliseconds"`
	// SleepMaxMilliseconds 是单次模拟延迟的硬上限，范围 1..60000。
	SleepMaxMilliseconds int64 `json:"sleep_max_milliseconds"`
	// TimeoutMilliseconds 限制每次调用，范围 1..60000；父 Context 可提前取消。
	TimeoutMilliseconds int64 `json:"timeout_milliseconds"`
	// ErrorRate 是等待完成后独立注入错误的概率，范围 0..1。
	ErrorRate float64 `json:"error_rate"`
}

// DefaultTestSourceConfig 默认调用一次，无延迟、无故障；时间预算均为 60 秒。
func DefaultTestSourceConfig() TestSourceConfig {
	return TestSourceConfig{Calls: 1, SleepMaxMilliseconds: 60000, TimeoutMilliseconds: 60000}
}

// Validate 拒绝非有限数及越界预算，不回显配置值。
func (c TestSourceConfig) Validate() error {
	if c.Calls < 1 || c.Calls > 16 {
		return fmt.Errorf("test datasource calls must be 1..16")
	}
	if c.SleepMaxMilliseconds < 1 || c.SleepMaxMilliseconds > 60000 || c.TimeoutMilliseconds < 1 || c.TimeoutMilliseconds > 60000 {
		return fmt.Errorf("test datasource sleep_max_milliseconds and timeout_milliseconds must be 1..60000")
	}
	for _, value := range []float64{c.SleepMeanMilliseconds, c.SleepStddevMilliseconds, c.ErrorRate} {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return fmt.Errorf("test datasource numeric settings must be finite")
		}
	}
	if c.SleepMeanMilliseconds < 0 || c.SleepMeanMilliseconds > float64(c.SleepMaxMilliseconds) || c.SleepStddevMilliseconds < 0 || c.SleepStddevMilliseconds > 60000 {
		return fmt.Errorf("test datasource sleep mean must be 0..sleep_max_milliseconds and stddev must be 0..60000")
	}
	if c.ErrorRate < 0 || c.ErrorRate > 1 {
		return fmt.Errorf("test datasource error_rate must be 0..1")
	}

	return nil
}

// TestRequest 携带单次模拟调用的租户和告警作用域，不参与业务身份生成。
type TestRequest struct {
	TenantID      string
	EventSourceID string
	AlertID       string
	CallIndex     int
	Config        TestSourceConfig
}

// TestSource 是负载模拟的窄调用端口，支持取消、超时和故障返回。
type TestSource interface {
	Call(ctx context.Context, request TestRequest) error
}

// CallTestSource 执行一次真实计数的模拟调用，不使用 Scope 的请求内缓存。
func (s *Scope) CallTestSource(ctx context.Context, config TestSourceConfig, index int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.sources.Test == nil {
		return fmt.Errorf("test datasource is unavailable")
	}
	return s.sources.Test.Call(ctx, TestRequest{TenantID: s.alert.BKTenantID, EventSourceID: s.alert.EventSourceID, AlertID: s.alert.AlertID, CallIndex: index, Config: config})
}
