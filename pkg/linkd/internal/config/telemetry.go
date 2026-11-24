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
)

const (
	// TelemetryExporterPrometheus 表示通过 Prometheus pull endpoint 导出 OTel 指标。
	TelemetryExporterPrometheus = "prometheus"
)

// TelemetryConfig 定义 Linkd 的 OpenTelemetry 配置。
type TelemetryConfig struct {
	Metrics   TelemetryMetricsConfig   `yaml:"metrics"`
	Profiling TelemetryProfilingConfig `yaml:"profiling"`
}

// TelemetryProfilingConfig 定义仅供现场诊断的 Go pprof 服务。默认关闭；开启时必须绑定回环地址，
// 避免将 goroutine、命令行和内存采样暴露到业务网络。
type TelemetryProfilingConfig struct {
	Enabled              bool   `yaml:"enabled"`
	ListenAddress        string `yaml:"listen_address"`
	BlockProfileRate     int    `yaml:"block_profile_rate"`
	MutexProfileFraction int    `yaml:"mutex_profile_fraction"`
}

// TelemetryMetricsConfig 定义指标 exporter。Exporter 为空表示不启用指标 SDK 和监听端口。
type TelemetryMetricsConfig struct {
	Exporter   string                    `yaml:"exporter"`
	Prometheus TelemetryPrometheusConfig `yaml:"prometheus"`
}

// TelemetryPrometheusConfig 定义当前 Linkd 进程的 Prometheus pull endpoint。
// 每个 Cleaner、Lifecycle、Control Plane 或 All-in-one 进程各自监听该地址；
// 多个拆分角色共享宿主网络时必须分别配置不冲突的端口。
type TelemetryPrometheusConfig struct {
	ListenAddress string `yaml:"listen_address"`
}

// Validate 校验已经声明的 telemetry 配置。
func (c TelemetryConfig) Validate() error {
	if c.Metrics.Exporter == "" {
		if c.Metrics.Prometheus != (TelemetryPrometheusConfig{}) {
			return fmt.Errorf("telemetry.metrics.exporter is required when prometheus endpoints are configured")
		}
		return c.Profiling.validate()
	}
	if c.Metrics.Exporter != TelemetryExporterPrometheus {
		return fmt.Errorf("telemetry.metrics.exporter must be %q: %q", TelemetryExporterPrometheus, c.Metrics.Exporter)
	}
	if c.Metrics.Prometheus.ListenAddress == "" {
		return fmt.Errorf("telemetry.metrics.prometheus.listen_address is required")
	}
	if err := validateTelemetryListenAddress(c.Metrics.Prometheus.ListenAddress); err != nil {
		return fmt.Errorf("telemetry.metrics.prometheus.listen_address: %w", err)
	}
	return c.Profiling.validate()
}

func (c TelemetryProfilingConfig) validate() error {
	if !c.Enabled {
		if c.ListenAddress != "" || c.BlockProfileRate != 0 || c.MutexProfileFraction != 0 {
			return fmt.Errorf("telemetry.profiling.enabled is required when profiling options are configured")
		}
		return nil
	}
	if c.ListenAddress == "" {
		return fmt.Errorf("telemetry.profiling.listen_address is required")
	}
	if err := validateTelemetryLoopbackListenAddress(c.ListenAddress); err != nil {
		return fmt.Errorf("telemetry.profiling.listen_address: %w", err)
	}
	if c.BlockProfileRate < 0 {
		return fmt.Errorf("telemetry.profiling.block_profile_rate must not be negative")
	}
	if c.MutexProfileFraction < 0 {
		return fmt.Errorf("telemetry.profiling.mutex_profile_fraction must not be negative")
	}
	return nil
}

// Enabled 报告指标 SDK 是否启用。
func (c TelemetryConfig) Enabled() bool {
	return c.Metrics.Exporter != ""
}

// ListenAddress 返回当前进程的 Prometheus 监听地址。
func (c TelemetryConfig) ListenAddress() string {
	if !c.Enabled() {
		return ""
	}
	return c.Metrics.Prometheus.ListenAddress
}

func validateTelemetryListenAddress(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil || host == "" || port == "" {
		return fmt.Errorf("must be host:port")
	}
	return nil
}

func validateTelemetryLoopbackListenAddress(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil || host == "" {
		return fmt.Errorf("must be host:port")
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("must bind an IP loopback address")
	}
	return nil
}
