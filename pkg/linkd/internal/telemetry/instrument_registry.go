// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package telemetry

import "go.opentelemetry.io/otel/metric"

type instrumentRegistry struct {
	meter       metric.Meter
	definitions []MetricDefinition
}

func (r *instrumentRegistry) Int64Counter(name string, info metricInfo, options ...metric.Int64CounterOption) (metric.Int64Counter, error) {
	cfg := metric.NewInt64CounterConfig(options...)
	instrument, err := r.meter.Int64Counter(name, options...)
	if err != nil {
		return nil, err
	}
	if err := r.register(name, "counter", cfg.Unit(), cfg.Description(), info); err != nil {
		return nil, err
	}
	return instrument, nil
}

func (r *instrumentRegistry) Int64UpDownCounter(name string, info metricInfo, options ...metric.Int64UpDownCounterOption) (metric.Int64UpDownCounter, error) {
	cfg := metric.NewInt64UpDownCounterConfig(options...)
	instrument, err := r.meter.Int64UpDownCounter(name, options...)
	if err != nil {
		return nil, err
	}
	if err := r.register(name, "up_down_counter", cfg.Unit(), cfg.Description(), info); err != nil {
		return nil, err
	}
	return instrument, nil
}

func (r *instrumentRegistry) Int64Gauge(name string, info metricInfo, options ...metric.Int64GaugeOption) (metric.Int64Gauge, error) {
	cfg := metric.NewInt64GaugeConfig(options...)
	instrument, err := r.meter.Int64Gauge(name, options...)
	if err != nil {
		return nil, err
	}
	if err := r.register(name, "gauge", cfg.Unit(), cfg.Description(), info); err != nil {
		return nil, err
	}
	return instrument, nil
}

func (r *instrumentRegistry) Float64Gauge(name string, info metricInfo, options ...metric.Float64GaugeOption) (metric.Float64Gauge, error) {
	cfg := metric.NewFloat64GaugeConfig(options...)
	instrument, err := r.meter.Float64Gauge(name, options...)
	if err != nil {
		return nil, err
	}
	if err := r.register(name, "gauge", cfg.Unit(), cfg.Description(), info); err != nil {
		return nil, err
	}
	return instrument, nil
}

func (r *instrumentRegistry) Int64Histogram(name string, info metricInfo, options ...metric.Int64HistogramOption) (metric.Int64Histogram, error) {
	cfg := metric.NewInt64HistogramConfig(options...)
	instrument, err := r.meter.Int64Histogram(name, options...)
	if err != nil {
		return nil, err
	}
	if err := r.register(name, "histogram", cfg.Unit(), cfg.Description(), info); err != nil {
		return nil, err
	}
	return instrument, nil
}

func (r *instrumentRegistry) Float64Histogram(name string, info metricInfo, options ...metric.Float64HistogramOption) (metric.Float64Histogram, error) {
	cfg := metric.NewFloat64HistogramConfig(options...)
	instrument, err := r.meter.Float64Histogram(name, options...)
	if err != nil {
		return nil, err
	}
	if err := r.register(name, "histogram", cfg.Unit(), cfg.Description(), info); err != nil {
		return nil, err
	}
	return instrument, nil
}
