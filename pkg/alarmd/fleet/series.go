// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"context"
	"time"
)

// SeriesPoint is one sample of one curve.
type SeriesPoint struct {
	AtUnixMilli int64   `json:"at"`
	Value       float64 `json:"value"`
}

// SeriesRange is what a provider returns for one expression.
type SeriesRange struct {
	Points []SeriesPoint
	// Code is the provider's own classification when it answered without
	// samples. Empty means the window really was empty.
	Code    string
	Message string
	Partial bool
}

// RangeProvider answers one fixed expression over a window.
//
// It is an interface so this package states what it needs and nothing more: the
// curves are alarmd's own numbers, and the page must not become a place where
// arbitrary queries can be posted.
type RangeProvider interface {
	Range(ctx context.Context, promQL string, start, end time.Time, step time.Duration) (SeriesRange, error)
}

// SeriesDefinition is one curve. The expression travels with the curve because
// a number on a page is only as trustworthy as the reader's ability to find out
// where it came from.
type SeriesDefinition struct {
	Key    string
	Label  string
	PromQL string
	// Help says what a reader should conclude from the curve, which is not the
	// same as what it measures.
	Help string
}

// seriesCatalog is fixed at build time. The page cannot add to it and cannot
// pass an expression: the expressions live next to the thresholds they
// illustrate, so a curve cannot drift from the judgment it explains.
var seriesCatalog = []SeriesDefinition{
	{
		Key: "expected", Label: "应有对象",
		// Reads the judgment's own exported number rather than the control plane
		// gauge it is derived from. The two are close but not the same read, and
		// a page that shows one figure beside the other contradicts itself the
		// moment they differ by a reporting cycle.
		PromQL: `max(bkmonitor_alarmd_fleet_objects{state="expected"})`,
		Help:   "控制面认为应该被执行的对象总数。它变动说明策略在增删，不是故障。",
	},
	{
		Key: "unknown", Label: "未知对象",
		PromQL: `max(bkmonitor_alarmd_fleet_objects{state="unknown"})`,
		Help:   "没人认领、或认领了但还说不出结论的对象。不为零时整体判定就停在 UNKNOWN。",
	},
	{
		Key: "anomalies", Label: "异常对象",
		PromQL: `sum(bkmonitor_alarmd_fleet_anomalies)`,
		Help:   "当前有多少对象处于异常。持续上升说明问题在扩散，不是单点抖动。",
	},
	{
		Key: "stalled", Label: "自锁对象",
		PromQL: `max(bkmonitor_alarmd_fleet_stalled_objects)`,
		Help:   "轮次已经停止结束的对象。这条不会自己回落，非零就需要有人介入。",
	},
}

// SeriesWindow is a bounded choice of range and resolution. The page picks from
// this list rather than sending its own, so a page cannot ask for a window that
// costs the provider more than the deployment intends to spend.
type SeriesWindow struct {
	Key      string
	Duration time.Duration
	Step     time.Duration
}

var seriesWindows = []SeriesWindow{
	{Key: "1h", Duration: time.Hour, Step: time.Minute},
	{Key: "6h", Duration: 6 * time.Hour, Step: 5 * time.Minute},
	{Key: "24h", Duration: 24 * time.Hour, Step: 10 * time.Minute},
}

// DefaultSeriesWindow is what the page opens with.
const DefaultSeriesWindow = "1h"

func lookupSeriesWindow(key string) (SeriesWindow, bool) {
	if key == "" {
		key = DefaultSeriesWindow
	}
	for _, window := range seriesWindows {
		if window.Key == key {
			return window, true
		}
	}
	return SeriesWindow{}, false
}

// Series is one curve as the page receives it.
type Series struct {
	Key    string        `json:"key"`
	Label  string        `json:"label"`
	PromQL string        `json:"promql"`
	Help   string        `json:"help"`
	Points []SeriesPoint `json:"points"`
	// Unavailable says the provider declined rather than reported nothing. The
	// two are different answers and the page must not merge them: an empty
	// curve reads as calm, a declined one reads as "you are not looking".
	Unavailable string `json:"unavailable,omitempty"`
	Detail      string `json:"detail,omitempty"`
	Partial     bool   `json:"partial,omitempty"`
}

type seriesResponse struct {
	Window       string   `json:"window"`
	WindowChoice []string `json:"window_choices"`
	StartUnixMs  int64    `json:"start"`
	EndUnixMs    int64    `json:"end"`
	StepSeconds  int      `json:"step_seconds"`
	Series       []Series `json:"series"`
}

// collectSeries runs the catalog over one window.
//
// One failing curve is reported on that curve and nowhere else. Failing the
// whole response would take three working curves down with the fourth, and the
// reader would lose the trend at the moment they came looking for it.
func collectSeries(
	ctx context.Context,
	provider RangeProvider,
	window SeriesWindow,
	at time.Time,
) []Series {
	start := at.Add(-window.Duration)
	collected := make([]Series, 0, len(seriesCatalog))
	for _, definition := range seriesCatalog {
		series := Series{
			Key: definition.Key, Label: definition.Label,
			PromQL: definition.PromQL, Help: definition.Help,
			Points: []SeriesPoint{},
		}
		result, err := provider.Range(ctx, definition.PromQL, start, at, window.Step)
		switch {
		case err != nil:
			series.Unavailable = "PROVIDER_ERROR"
			series.Detail = err.Error()
		case result.Code != "":
			series.Unavailable = result.Code
			series.Detail = result.Message
		default:
			if result.Points != nil {
				series.Points = result.Points
			}
			series.Partial = result.Partial
		}
		collected = append(collected, series)
	}
	return collected
}
