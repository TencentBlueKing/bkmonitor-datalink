// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package telemetry 负责 Linkd OpenTelemetry Provider、Prometheus 导出和低基数业务指标。
package telemetry

import (
	"fmt"
	"net"
)

const (
	// ExporterPrometheus 表示通过 Prometheus pull endpoint 导出 OTel 指标。
	ExporterPrometheus = "prometheus"
)

// Role 标识一个 Linkd 进程职责，用于 Resource 属性和运行态展示。
type Role string

const (
	RoleCleaner      Role = "cleaner"
	RoleLifecycle    Role = "lifecycle"
	RoleControlPlane Role = "control-plane"
	RoleAllInOne     Role = "all-in-one"
)

// Config 定义 Linkd 的 OpenTelemetry 配置。
type Config struct {
	Metrics   MetricsConfig   `yaml:"metrics"`
	Profiling ProfilingConfig `yaml:"profiling"`
}

// ProfilingConfig 定义仅供现场诊断的 Go pprof 服务。默认关闭；开启时必须绑定回环地址，
// 避免将 goroutine、命令行和内存采样暴露到业务网络。
type ProfilingConfig struct {
	Enabled              bool   `yaml:"enabled"`
	ListenAddress        string `yaml:"listen_address"`
	BlockProfileRate     int    `yaml:"block_profile_rate"`
	MutexProfileFraction int    `yaml:"mutex_profile_fraction"`
}

// MetricsConfig 定义指标 exporter。Exporter 为空表示不启用指标 SDK 和监听端口。
type MetricsConfig struct {
	Exporter   string           `yaml:"exporter"`
	Prometheus PrometheusConfig `yaml:"prometheus"`
}

// PrometheusConfig 定义当前 Linkd 进程的 Prometheus pull endpoint。
// 每个 Cleaner、Lifecycle、Control Plane 或 All-in-one 进程各自监听该地址；
// 多个拆分角色共享宿主网络时必须分别配置不冲突的端口。
type PrometheusConfig struct {
	ListenAddress string `yaml:"listen_address"`
}

// Validate 校验已经声明的 telemetry 配置。
func (c Config) Validate() error {
	if c.Metrics.Exporter == "" {
		if c.Metrics.Prometheus != (PrometheusConfig{}) {
			return fmt.Errorf("telemetry.metrics.exporter is required when prometheus endpoints are configured")
		}
		return c.Profiling.validate()
	}
	if c.Metrics.Exporter != ExporterPrometheus {
		return fmt.Errorf("telemetry.metrics.exporter must be %q: %q", ExporterPrometheus, c.Metrics.Exporter)
	}
	if c.Metrics.Prometheus.ListenAddress == "" {
		return fmt.Errorf("telemetry.metrics.prometheus.listen_address is required")
	}
	if err := validateListenAddress(c.Metrics.Prometheus.ListenAddress); err != nil {
		return fmt.Errorf("telemetry.metrics.prometheus.listen_address: %w", err)
	}
	return c.Profiling.validate()
}

func (c ProfilingConfig) validate() error {
	if !c.Enabled {
		if c.ListenAddress != "" || c.BlockProfileRate != 0 || c.MutexProfileFraction != 0 {
			return fmt.Errorf("telemetry.profiling.enabled is required when profiling options are configured")
		}
		return nil
	}
	if c.ListenAddress == "" {
		return fmt.Errorf("telemetry.profiling.listen_address is required")
	}
	if err := validateLoopbackListenAddress(c.ListenAddress); err != nil {
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
func (c Config) Enabled() bool {
	return c.Metrics.Exporter != ""
}

// ListenAddress 返回当前进程的 Prometheus 监听地址。
func (c Config) ListenAddress() string {
	if !c.Enabled() {
		return ""
	}
	return c.Metrics.Prometheus.ListenAddress
}

func validateListenAddress(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil || host == "" || port == "" {
		return fmt.Errorf("must be host:port")
	}
	return nil
}

func validateLoopbackListenAddress(address string) error {
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
