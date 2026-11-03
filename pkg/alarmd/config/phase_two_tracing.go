// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import (
	"fmt"
	"math"
	"net/url"
)

// The span export's defaults: one Slot in ten is a candidate, and at most
// five traces a second go out whatever the Slot count.
const (
	DefaultTraceSampleRatio        = 0.1
	DefaultTraceMaxTracesPerSecond = 5
)

// PhaseTwoTracingConfig is alarmd's own span export: an alarmd.slot span for
// a sampled Slot and an alarmd.query span for each of its query attempts,
// sent over OTLP/HTTP. An empty endpoint is off; the query service is then
// sent a fresh trace id per attempt with the sampled flag unset.
type PhaseTwoTracingConfig struct {
	// OTLPEndpoint is the collector, http(s)://host:port; spans go to
	// /v1/traces on it.
	OTLPEndpoint string `yaml:"otlp_endpoint"`
	// Token is the APM application's token, carried as the resource
	// attribute bk.data.token; TokenFile is its reference, read once at
	// startup, so a rotation needs a restart. Neither is ever shown.
	Token     string `yaml:"token" json:"-"`
	TokenFile string `yaml:"token_file"`
	// SampleRatio is the share of Slots that are candidates, 0 to 1;
	// absent is DefaultTraceSampleRatio.
	SampleRatio *float64 `yaml:"sample_ratio,omitempty"`
	// MaxTracesPerSecond bounds the sampled Slots a second; absent is
	// DefaultTraceMaxTracesPerSecond.
	MaxTracesPerSecond *int `yaml:"max_traces_per_second,omitempty"`
}

func (c PhaseTwoTracingConfig) validate() error {
	if c.OTLPEndpoint != "" {
		parsed, err := url.Parse(c.OTLPEndpoint)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil ||
			(parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
			return fmt.Errorf("phase_two.tracing.otlp_endpoint must be an http or https URL naming a host and nothing " +
				"else, such as http://collector:4318; spans are sent to /v1/traces on it")
		}
	}
	if c.SampleRatio != nil && (math.IsNaN(*c.SampleRatio) || *c.SampleRatio < 0 || *c.SampleRatio > 1) {
		return fmt.Errorf("phase_two.tracing.sample_ratio %v must be within 0 and 1", *c.SampleRatio)
	}
	if c.MaxTracesPerSecond != nil && *c.MaxTracesPerSecond < 1 {
		return fmt.Errorf("phase_two.tracing.max_traces_per_second %d must be a positive number of traces", *c.MaxTracesPerSecond)
	}
	return nil
}

// TraceSampleRatio is the share of Slots that are sampling candidates.
func (c Config) TraceSampleRatio() float64 {
	if c.PhaseTwo.Tracing.SampleRatio == nil {
		return DefaultTraceSampleRatio
	}
	return *c.PhaseTwo.Tracing.SampleRatio
}

// TraceMaxTracesPerSecond bounds the sampled Slots a second.
func (c Config) TraceMaxTracesPerSecond() int {
	if c.PhaseTwo.Tracing.MaxTracesPerSecond == nil {
		return DefaultTraceMaxTracesPerSecond
	}
	return *c.PhaseTwo.Tracing.MaxTracesPerSecond
}
