// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// An item's detect_interval is how often it is detected, apart from how long
// a window each detection aggregates. Every interval-like quantity used to be
// the aggregation interval; with the field, the detection step fills the
// schedule, the trigger's step and every quantity counted in detections, and
// the aggregation interval keeps the query window.
//
// A step that ends up equal to the aggregation interval is no step at all.
// That is decided here, before anything is derived, so that a writer stating
// the value the item would have had anyway, or a step this build cannot run
// as written, changes no Query Group, schedule or state generation.

// detectIntervalFloorSeconds is the shortest step a Plan runs at, the
// shortest aggregation interval the platform's own deployments run. It is a
// protection of the system, not a meaning: a shorter step runs at the floor
// and says so.
const detectIntervalFloorSeconds = 10

const (
	// ReasonDetectIntervalInvalid refuses a detect_interval nothing can run
	// on: not a number, not an integer, or not positive.
	ReasonDetectIntervalInvalid = "DETECT_INTERVAL_INVALID"
	// ReasonDetectIntervalAboveAgg names a step longer than the aggregation
	// interval: each detection reads only the latest aggregation window,
	// and the windows between two detections are never detected.
	ReasonDetectIntervalAboveAgg = "ABOVE_AGG_INTERVAL"
	// ReasonDetectIntervalNotDivisible names a step that does not divide the
	// aggregation interval: it runs as written, and the compatible output,
	// which sends only detections on the aggregation grid, gets sparse.
	ReasonDetectIntervalNotDivisible = "NOT_DIVISIBLE"
	// ReasonDetectIntervalBelowFloor names a step under the floor, run at the
	// floor.
	ReasonDetectIntervalBelowFloor = "BELOW_FLOOR"
	// ReasonDetectIntervalSourceNotSliding names an item whose queries read
	// a source whose storage buckets on the aggregation grid whatever start
	// it is asked for (search engine, SQL, PromQL): it runs at the
	// aggregation interval, as if the field were absent.
	ReasonDetectIntervalSourceNotSliding = "SOURCE_NOT_SLIDING"
	// ReasonDetectIntervalAlgorithmNotSliding names an item a Level of which
	// detects with an algorithm that cannot run at the step: AdvancedRingRatio
	// needs history points a step apart, which windows an aggregation
	// interval long cannot place, and OsRestart compares with points 10 and
	// 25 minutes back that a step of 10 minutes or more would put out of
	// order. It runs at the aggregation interval, as if the field were absent.
	ReasonDetectIntervalAlgorithmNotSliding = "ALGORITHM_NOT_SLIDING"
)

// DetectIntervalWarnings are the reasons a detect_interval that runs is
// noted under. They are CONFIG_NORMALIZED dispositions: the Plan runs, and
// the fleet lists them under their own check.
var DetectIntervalWarnings = []string{
	ReasonDetectIntervalAboveAgg, ReasonDetectIntervalNotDivisible, ReasonDetectIntervalBelowFloor,
	ReasonDetectIntervalSourceNotSliding, ReasonDetectIntervalAlgorithmNotSliding,
}

// slidingSourceSemantics are the query sources a step shorter than the
// aggregation interval can read: the structured time series the query
// service evaluates through its PromQL engine, which starts a bucket where
// the request starts. A search engine's date histogram, the computing
// platform's SQL and a pushed-down InfluxDB GROUP BY time() bucket on the
// epoch grid whatever start they are asked for, and a PromQL strategy keeps
// its window in the owner's expression, where no offset can be added.
var slidingSourceSemantics = map[string]struct{}{
	"bk_monitor/time_series": {},
	"custom/time_series":     {},
}

// detectStep is the step an item is detected at.
type detectStep struct {
	// Seconds is the step. It is the aggregation interval when the item
	// configures none, configures one equal to it, or configures one this
	// build cannot run as written.
	Seconds int64
	// Configured is a step that differs from the aggregation interval. Only
	// then does anything compile differently from an item without the field.
	Configured bool
	// Written is the value the item carried, 0 when it carried none.
	Written int64
	// Warning is one of DetectIntervalWarnings, or empty.
	Warning string
	// Detail says what the warning is about beyond the numbers: the source
	// or the algorithm a step could not run on.
	Detail string
}

// itemDetectStep reads an item's detect_interval against its aggregation
// interval.
func itemDetectStep(item legacyItem, aggregation int64) (detectStep, error) {
	raw := strings.TrimSpace(string(item.DetectInterval))
	if raw == "" || raw == "null" {
		return detectStep{Seconds: aggregation}, nil
	}
	written, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || written <= 0 {
		return detectStep{}, fmt.Errorf("detect_interval=%s is not a positive integer", truncatedDetectInterval(item.DetectInterval))
	}
	step := detectStep{Seconds: written, Written: written}
	if written < detectIntervalFloorSeconds {
		step.Seconds, step.Warning = detectIntervalFloorSeconds, ReasonDetectIntervalBelowFloor
	}
	if step.Seconds == aggregation {
		return step, nil
	}
	if reason, detail := notSliding(item, step.Seconds); reason != "" {
		return detectStep{Seconds: aggregation, Written: written, Warning: reason, Detail: detail}, nil
	}
	if step.Warning == "" {
		switch {
		case step.Seconds > aggregation:
			step.Warning = ReasonDetectIntervalAboveAgg
		case aggregation%step.Seconds != 0:
			step.Warning = ReasonDetectIntervalNotDivisible
		}
	}
	step.Configured = true
	return step, nil
}

// osRestartFixedOffsetSeconds is the nearer of the two fixed history points
// OsRestart compares with; its previous detection has to be nearer still.
const osRestartFixedOffsetSeconds = 600

// notSliding names why an item cannot run at step, a step other than its
// aggregation interval, the empty reason when it can.
func notSliding(item legacyItem, step int64) (string, string) {
	for _, raw := range item.QueryConfigs {
		var query struct {
			DataSourceLabel string `json:"data_source_label"`
			DataTypeLabel   string `json:"data_type_label"`
		}
		if json.Unmarshal(raw, &query) != nil {
			return ReasonDetectIntervalSourceNotSliding, "source=unreadable"
		}
		semantics := query.DataSourceLabel + "/" + query.DataTypeLabel
		if _, sliding := slidingSourceSemantics[semantics]; !sliding {
			return ReasonDetectIntervalSourceNotSliding, "source=" + semantics
		}
	}
	for _, algorithm := range item.Algorithms {
		if algorithm.Type == strategy.DetectorKindAdvancedRingRatio ||
			algorithm.Type == strategy.DetectorKindOsRestart && step >= osRestartFixedOffsetSeconds {
			return ReasonDetectIntervalAlgorithmNotSliding, "algorithm=" + algorithm.Type
		}
	}
	return "", ""
}

// warningDisposition is the CONFIG_NORMALIZED disposition a step that runs
// with a warning is listed under, nil for one without.
func (step detectStep) warningDisposition(sourceID string, aggregation int64) *ObjectDisposition {
	if step.Warning == "" {
		return nil
	}
	detail := fmt.Sprintf("detect_interval=%d step=%d agg_interval=%d", step.Written, step.Seconds, aggregation)
	if step.Detail != "" {
		detail += " " + step.Detail
	}
	return &ObjectDisposition{SourceID: sourceID, Scope: "PLAN", Disposition: DispositionConfigNormalized, Reason: step.Warning,
		FieldPath: "items[0].detect_interval", Detail: detail}
}

// queryDelayUnit is what a configured step rounds the query delay up to:
// the step, or the aggregation interval when the step is the longer one, so
// a warned long step does not stretch a short delay to a whole step.
func queryDelayUnit(step, aggregation int64) int64 {
	if step > 0 && step < aggregation {
		return step
	}
	return aggregation
}

// slidingClauseOffset is the offset a clause of a configured Plan is read
// with. A query aligned to its step has the query service shift every point
// one step less a millisecond forward, so the point at t is the bucket
// [t, t+step); an unaligned query, the only kind that starts a bucket where
// the request starts, gets no such shift and labels each point with the end
// of its bucket. The configured Plan asks for the same shift itself, composed
// with the clause's own time shift: offset is the clause's, forward its
// direction, and the result is again an offset and a direction.
func slidingClauseOffset(offset string, forward bool, stepSeconds int64) (string, bool, error) {
	own := time.Duration(0)
	if offset != "" {
		parsed, err := time.ParseDuration(offset)
		if err != nil {
			return "", false, fmt.Errorf("clause offset %q: %w", offset, err)
		}
		own = parsed
	}
	// Backward is positive, as the query service adds it.
	net := own
	if forward {
		net = -own
	}
	net -= time.Duration(stepSeconds)*time.Second - time.Millisecond
	if net >= 0 {
		return strconv.FormatInt(net.Milliseconds(), 10) + "ms", false, nil
	}
	return strconv.FormatInt((-net).Milliseconds(), 10) + "ms", true, nil
}

func truncatedDetectInterval(raw json.RawMessage) string {
	const limit = 32
	if len(raw) > limit {
		return string(raw[:limit]) + "..."
	}
	return string(raw)
}
