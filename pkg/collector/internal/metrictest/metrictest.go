// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package metrictest reads self-monitoring series back from the default
// gatherer, the registry /metrics serves, for the tests of the packages that
// write them. It imports nothing of the collector, so any of them can use it.
package metrictest

import (
	"sort"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// Value returns the value of the counter, gauge or untyped series of the
// family name whose labels are exactly labels, and whether that series exists.
func Value(t testing.TB, name string, labels map[string]string) (float64, bool) {
	t.Helper()
	for _, metric := range family(t, name) {
		if !sameLabels(metric.GetLabel(), labels) {
			continue
		}
		switch {
		case metric.Counter != nil:
			return metric.GetCounter().GetValue(), true
		case metric.Gauge != nil:
			return metric.GetGauge().GetValue(), true
		case metric.Untyped != nil:
			return metric.GetUntyped().GetValue(), true
		}
	}
	return 0, false
}

// Count returns the sample count and sum of the histogram series of the
// family name whose labels are exactly labels, and whether it exists.
func Count(t testing.TB, name string, labels map[string]string) (uint64, float64, bool) {
	t.Helper()
	for _, metric := range family(t, name) {
		if sameLabels(metric.GetLabel(), labels) && metric.Histogram != nil {
			return metric.GetHistogram().GetSampleCount(), metric.GetHistogram().GetSampleSum(), true
		}
	}
	return 0, 0, false
}

// Series lists the series of the family name, each as its labels written
// name=value and joined by commas in label order, sorted.
func Series(t testing.TB, name string) []string {
	t.Helper()
	var series []string
	for _, metric := range family(t, name) {
		pairs := make([]string, 0, len(metric.GetLabel()))
		for _, label := range metric.GetLabel() {
			pairs = append(pairs, label.GetName()+"="+label.GetValue())
		}
		series = append(series, strings.Join(pairs, ","))
	}
	sort.Strings(series)
	return series
}

func family(t testing.TB, name string) []*dto.Metric {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range families {
		if f.GetName() == name {
			return f.GetMetric()
		}
	}
	return nil
}

func sameLabels(got []*dto.LabelPair, want map[string]string) bool {
	if len(got) != len(want) {
		return false
	}
	for _, label := range got {
		if v, ok := want[label.GetName()]; !ok || v != label.GetValue() {
			return false
		}
	}
	return true
}
