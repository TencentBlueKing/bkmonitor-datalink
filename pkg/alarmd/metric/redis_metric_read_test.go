// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package metric

import (
	"testing"

	dto "github.com/prometheus/client_model/go"
)

func redisSamples(t *testing.T, recorder *Recorder, family string, labels map[string]string) []*dto.Metric {
	t.Helper()
	families, err := recorder.Gatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	var samples []*dto.Metric
	for _, candidate := range families {
		if candidate.GetName() != family {
			continue
		}
	next:
		for _, sample := range candidate.GetMetric() {
			for name, value := range labels {
				if labelOf(sample, name) != value {
					continue next
				}
			}
			samples = append(samples, sample)
		}
	}
	return samples
}

func redisHistogramCount(t *testing.T, recorder *Recorder, family string, labels map[string]string) float64 {
	t.Helper()
	total := 0.0
	for _, sample := range redisSamples(t, recorder, family, labels) {
		total += float64(sample.GetHistogram().GetSampleCount())
	}
	return total
}

func redisHistogramSum(t *testing.T, recorder *Recorder, family string, labels map[string]string) float64 {
	t.Helper()
	total := 0.0
	for _, sample := range redisSamples(t, recorder, family, labels) {
		total += sample.GetHistogram().GetSampleSum()
	}
	return total
}

func redisCounterSum(t *testing.T, recorder *Recorder, family string, labels map[string]string) float64 {
	t.Helper()
	total := 0.0
	for _, sample := range redisSamples(t, recorder, family, labels) {
		total += sample.GetCounter().GetValue()
	}
	return total
}

func redisLabelValues(t *testing.T, recorder *Recorder, family, label string) []string {
	t.Helper()
	var values []string
	for _, sample := range redisSamples(t, recorder, family, nil) {
		values = append(values, labelOf(sample, label))
	}
	return values
}
