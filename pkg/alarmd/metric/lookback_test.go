// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import (
	"context"
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/lookback"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/readhold"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
)

// lookbackSources is how many source labels a process counts: every source a
// query can be compiled from, and mixed and other.
func lookbackSources() int { return len(controlplane.SupportedSourceSemantics) + 2 }

// lookbackSeriesUpperBounds is each lookback family's series count once
// bound, by the family's short name.
func lookbackSeriesUpperBounds() map[string]int {
	sources := lookbackSources()
	return map[string]int{
		"read_hold_transition_total":               1,
		"read_hold_transition_overtaken_total":     1,
		"read_hold_predecessor_total":              len(readhold.PredecessorReasons) + len(readhold.LinkSkipReasons),
		"read_hold_transition_clamped_total":       len(readhold.ClampSources),
		"read_hold_record_corrupt_total":           1,
		"read_hold_retire_close_failed_total":      1,
		"read_hold_close_previous_skipped_total":   1,
		"read_hold_degraded_total":                 len(readhold.DegradedReasons),
		"read_hold_groups":                         len(lookback.Sources) * 3,
		"read_hold_max_seconds":                    len(lookback.Sources),
		"lookback_first_reads_total":               sources,
		"lookback_rechecks_total":                  sources * len(lookback.RecheckOutcomes),
		"lookback_changed_windows_total":           sources,
		"lookback_changes_total":                   sources * len(lookback.Changes),
		"lookback_sample_classes_total":            sources * len(lookback.SampleClasses),
		"lookback_read_early_groups":               sources,
		"lookback_series_late_groups":              sources,
		"lookback_supplement_windows_total":        sources * len(lookback.DirectedOutcomes),
		"lookback_supplement_unobserved_total":     sources * len(lookback.DirectedUnobservedReasons),
		"lookback_supplement_series_total":         sources * len(lookback.SupplementSeriesOutcomes),
		"lookback_supplement_points_total":         sources,
		"lookback_directed_read_bytes_total":       sources,
		"lookback_supplement_hold_total":           sources * len(lookback.SupplementHoldBuckets),
		"lookback_supplement_hold_max_seconds":     sources,
		"lookback_directed_early_total":            sources * len(lookback.EarlyGroups),
		"lookback_directed_early_undecided_total":  sources,
		"lookback_directed_early_read_bytes_total": sources,
		"lookback_earlier_read_bytes_total":        sources,
		"lookback_read_hold_ignored_total":         sources * (len(lookback.ReadHoldIgnoredReasons) - 1),
		"lookback_first_read_bytes_total":          sources,
		"lookback_recheck_bytes_total":             sources,
		"lookback_unknown_lookback_total":          sources,
		"lookback_coverage":                        3,
		"lookback_pending":                         2,
		"lookback_preemptions_total":               sources,
		// Every reason the scheduler refuses with, and other.
		"lookback_permit_refusals_total": len(scheduler.LookbackRefusals) + 1,
		"lookback_faults_total":          len(lookback.Faults),
	}
}

// Not running emits nothing; running emits every cell of a production
// engine's counts, zero included, and each count under its own words.
func TestTheLookbackCollectorEmitsEveryCellOnceBound(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	if series := gatherFamily(t, r, "bkmonitor_alarmd_lookback_first_reads_total"); len(series) != 0 {
		t.Fatalf("a process not running the lookback emitted %v", series)
	}
	engine, err := lookback.New(lookback.Options{Refusals: scheduler.LookbackRefusals,
		Recheck: func(context.Context, execution.PhysicalQuerySpec, execution.ProviderSeriesSink) (execution.ProviderCompletion, error) {
			return execution.ProviderCompletion{}, nil
		},
		Permit: func() (func(), <-chan struct{}, string) { return nil, nil, scheduler.LookbackRefusedWaiting },
		Owns:   func(execution.QueryGroupIdentity) bool { return true }, Owned: func() int { return 7 }})
	if err != nil {
		t.Fatal(err)
	}
	stats := engine.Stats()
	logs := controlplane.SupportedSourceSemantics[1]
	stats.Sources[logs].Samples[lookback.OutcomeCaptured] = 4
	stats.Sources[logs].Changes[lookback.RungNames[1]][lookback.ChangePointsAdded] = 7
	stats.Sources[logs].Changes[lookback.RungNames[3]][lookback.ChangePointsAdded] = 2
	stats.Sources[logs].Rechecks[lookback.RungNames[1]][lookback.RecheckOutcomes[0]] = 4
	stats.Sources[logs].Rechecks[lookback.RungNames[2]][lookback.RecheckOutcomes[0]] = 6
	stats.Sources[logs].ChangedWindows[lookback.RungNames[0]] = 1
	stats.Sources[logs].ChangedWindows[lookback.RungNames[4]] = 2
	stats.Sources[logs].Completion["le_300s"] = 2
	stats.Sources[logs].Preempted[lookback.RungNames[0]] = 5
	stats.Sources[logs].Preempted[lookback.RungNames[2]] = 1
	stats.Sources[logs].Probes[lookback.ProbeChanged] = 6
	stats.Sources[logs].EmptyFirstReads[lookback.EmptyArrived] = 8
	stats.Sources[logs].EmptyFirstReadCompletion["le_600s"] = 3
	entry := stats.Sources[logs]
	entry.RecheckBytes = 2048
	entry.SupplementHold["le_5s"] = 11
	entry.SupplementHoldMaxSeconds = 3.5
	entry.EarlyReads[lookback.EarlyBeforeNext] = 12
	entry.EarlyReads[lookback.EarlyOvertaken] = 3
	entry.EarlyReads[lookback.EarlyFailed] = 1
	entry.EarlyReads[lookback.EarlyNothingLate] = 2
	entry.ReadHoldIgnored[lookback.ClassPartialRevised] = 5
	entry.ReadHoldIgnored[lookback.IgnoredNoWholeWindowArrival] = 7
	entry.EarlyUndecided = 13
	entry.EarlyReadBytes = 14
	stats.Sources[logs] = entry
	stats.PermitRefusals[scheduler.LookbackRefusedWaiting] = 9
	stats.Pending, stats.PendingBytes = 3, 4096
	stats.ReadHoldGroups = map[string]lookback.ReadHoldGroups{}
	for _, source := range lookback.Sources {
		stats.ReadHoldGroups[source] = lookback.ReadHoldGroups{Held: 1, MaxMillis: 2000, MaxKnown: true}
	}
	r.SetLookbackSource(func() lookback.Stats { return stats })
	for name, n := range lookbackSeriesUpperBounds() {
		if got := len(gatherFamily(t, r, "bkmonitor_alarmd_"+name)); got != n {
			t.Fatalf("%s has %d series, want every cell: %d", name, got, n)
		}
	}
	value := func(family string, want map[string]string) float64 {
		for _, m := range gatherFamily(t, r, family) {
			matched := true
			for _, label := range m.GetLabel() {
				if expected, named := want[label.GetName()]; named && expected != label.GetValue() {
					matched = false
				}
			}
			if matched {
				if m.GetCounter() != nil {
					return m.GetCounter().GetValue()
				}
				return m.GetGauge().GetValue()
			}
		}
		t.Fatalf("%s has no series %v", family, want)
		return 0
	}
	// The rungs add up: each rung's counts are /api/lookback's.
	if got := value("bkmonitor_alarmd_lookback_changes_total", map[string]string{"source": logs, "class": lookback.ChangePointsAdded}); got != 9 {
		t.Fatalf("points added over the rungs = %v, want 9", got)
	}
	if got := value("bkmonitor_alarmd_lookback_rechecks_total", map[string]string{"source": logs, "outcome": lookback.RecheckOutcomes[0]}); got != 10 {
		t.Fatalf("%s rechecks over the rungs = %v, want 10", lookback.RecheckOutcomes[0], got)
	}
	if got := value("bkmonitor_alarmd_lookback_changed_windows_total", map[string]string{"source": logs}); got != 3 {
		t.Fatalf("changed windows over the rungs = %v, want 3", got)
	}
	if got := value("bkmonitor_alarmd_lookback_pending", map[string]string{"what": "bytes"}); got != 4096 {
		t.Fatalf("pending bytes = %v", got)
	}
	if got := value("bkmonitor_alarmd_lookback_preemptions_total", map[string]string{"source": logs}); got != 6 {
		t.Fatalf("preemptions over the rungs = %v, want 6", got)
	}
	if got := value("bkmonitor_alarmd_lookback_permit_refusals_total", map[string]string{"reason": scheduler.LookbackRefusedWaiting}); got != 9 {
		t.Fatalf("refusals = %v", got)
	}
	if got := value("bkmonitor_alarmd_lookback_coverage", map[string]string{"what": "owned"}); got != 7 {
		t.Fatalf("owned = %v", got)
	}
	if got := value("bkmonitor_alarmd_lookback_recheck_bytes_total", map[string]string{"source": logs}); got != 2048 {
		t.Fatalf("recheck bytes = %v", got)
	}
	if got := value("bkmonitor_alarmd_lookback_supplement_hold_total", map[string]string{"source": logs, "bucket": "le_5s"}); got != 11 {
		t.Fatalf("supplement holds up to 5s = %v", got)
	}
	if got := value("bkmonitor_alarmd_lookback_supplement_hold_max_seconds", map[string]string{"source": logs}); got != 3.5 {
		t.Fatalf("longest supplement hold = %v", got)
	}
	// The early reads by what the mechanism is read by: before_next over
	// before_next and not_ahead.
	for group, want := range map[string]float64{lookback.EarlyGroupBeforeNext: 12, lookback.EarlyGroupNotAhead: 4, lookback.EarlyGroupOutside: 2} {
		if got := value("bkmonitor_alarmd_lookback_directed_early_total", map[string]string{"source": logs, "group": group}); got != want {
			t.Fatalf("early reads %s = %v, want %v", group, got, want)
		}
	}
	for _, m := range gatherFamily(t, r, "bkmonitor_alarmd_lookback_read_hold_ignored_total") {
		for _, label := range m.GetLabel() {
			if label.GetName() == "reason" && label.GetValue() == lookback.ClassPartialRevised {
				t.Fatal("partial_revised is counted twice: here and in lookback_sample_classes_total")
			}
		}
	}
	if got := value("bkmonitor_alarmd_lookback_read_hold_ignored_total", map[string]string{"source": logs, "reason": lookback.IgnoredNoWholeWindowArrival}); got != 7 {
		t.Fatalf("no whole-window arrival = %v, want 7", got)
	}
	if got := value("bkmonitor_alarmd_lookback_directed_early_undecided_total", map[string]string{"source": logs}); got != 13 {
		t.Fatalf("early undecided pairs = %v", got)
	}
	if got := value("bkmonitor_alarmd_lookback_directed_early_read_bytes_total", map[string]string{"source": logs}); got != 14 {
		t.Fatalf("early read bytes = %v", got)
	}
}

// The read hold gauges say what a replica holds and nothing else: none held,
// no series; a source whose groups' holds are all unknown, its counts and
// no largest hold; and each count under its own kind, the hold in seconds.
func TestTheReadHoldGaugesAreAbsentWhereNothingIsKnown(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	stats := lookback.Stats{}
	r.SetLookbackSource(func() lookback.Stats { return stats })
	for _, family := range []string{"bkmonitor_alarmd_read_hold_groups", "bkmonitor_alarmd_read_hold_max_seconds"} {
		if series := gatherFamily(t, r, family); len(series) != 0 {
			t.Fatalf("a replica holding no group emitted %s %v", family, series)
		}
	}
	logs := controlplane.SupportedSourceSemantics[1]
	stats.ReadHoldGroups = map[string]lookback.ReadHoldGroups{
		lookback.SourceOther: {Unknown: 4},
		logs:                 {Held: 2, AtLimit: 1, Unknown: 1, MaxMillis: 308_500, MaxKnown: true},
	}
	groups := map[string]float64{}
	for _, m := range gatherFamily(t, r, "bkmonitor_alarmd_read_hold_groups") {
		labels := map[string]string{}
		for _, label := range m.GetLabel() {
			labels[label.GetName()] = label.GetValue()
		}
		groups[labels["source"]+"/"+labels["kind"]] = m.GetGauge().GetValue()
	}
	want := map[string]float64{lookback.SourceOther + "/held": 0, lookback.SourceOther + "/at_limit": 0, lookback.SourceOther + "/unknown": 4,
		logs + "/held": 2, logs + "/at_limit": 1, logs + "/unknown": 1}
	if !reflect.DeepEqual(groups, want) {
		t.Fatalf("groups %v, want %v", groups, want)
	}
	maxima := gatherFamily(t, r, "bkmonitor_alarmd_read_hold_max_seconds")
	if len(maxima) != 1 || maxima[0].GetGauge().GetValue() != 308.5 || maxima[0].GetLabel()[0].GetValue() != logs {
		t.Fatalf("maxima %v, want only %s at 308.5 s", maxima, logs)
	}
}
