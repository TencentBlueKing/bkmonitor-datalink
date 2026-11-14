// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/access"
	accessuq "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/access/uq"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/testwait"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

const (
	supplementHostA = "127.0.0.1"
	supplementHostB = "127.0.0.2"
)

// recheckedRead is the read a supplement runs on, read again from the
// query service as the lookback reads one (uq.Client.Recheck), under the
// deadline of the context the supplement is run with.
type recheckedRead struct{ client *accessuq.Client }

func (read recheckedRead) Keeps(execution.PhysicalQueryDigest) bool { return true }

func (read recheckedRead) Replay(ctx context.Context, spec execution.PhysicalQuerySpec, sink execution.ProviderSeriesSink) (execution.ProviderCompletion, error) {
	return read.client.Recheck(ctx, spec, sink)
}

// firingFixture is the cutover fixture with strategy 1001 firing on every
// Slot, so that each series it evaluates writes an event naming its digest.
func firingFixture(t *testing.T) *cutoverStallFixture {
	t.Helper()
	return startCutoverFixtureWith(t, nil, observability.Discard(observability.ComponentRuntime), func(ctx context.Context, client *redis.Client, _ config.Config) {
		installEditedStrategies(t, ctx, client, 1725000000, thresholdTo(1))
	})
}

// seriesDigests is each host's series digest, from the events a Slot wrote.
func seriesDigests(fixture *cutoverStallFixture, slot execution.EvaluationTime) map[string]execution.SeriesIdentityDigest {
	digests := map[string]execution.SeriesIdentityDigest{}
	for _, event := range fixture.eventsAt(slot) {
		for _, dimension := range event.RecordRef.Dimensions {
			for _, host := range []string{supplementHostA, supplementHostB} {
				if strings.Contains(string(dimension), `"`+host+`"`) {
					digests[host] = execution.SeriesIdentityDigest(event.RecordRef.DimensionIdentityDigest)
				}
			}
		}
	}
	return digests
}

// After an edit, a supplement of a Slot from before it is written (design
// 013 section 3.2): the Plan never left, and the Slot's own Segment, closed
// by the edit, answers the Plan with the record it ran under. A series the
// Slot read without - its data landed late - is admitted, its event and its
// state written at that Slot; one the Slot did read has its state there and
// is not decided twice.
func TestASupplementOfASlotBeforeAnEditIsWritten(t *testing.T) {
	// The digests the hosts' series go by, from a run where both report.
	probe := firingFixture(t)
	probe.uqHosts = func(int64) []string { return []string{supplementHostA, supplementHostB} }
	probeSlot := execution.EvaluationTime(probe.base + 60)
	if ended := runSlot(t, probe, probe.queryGroup, probe.runner, probeSlot); ended != fullEnding {
		t.Fatalf("the probe Slot ended %q", ended)
	}
	digests := seriesDigests(probe, probeSlot)
	if len(digests) != 2 {
		t.Fatalf("probe digests %v, want one per host", digests)
	}

	fixture := firingFixture(t)
	ctx := context.Background()
	if own := seriesDigests(fixture, execution.EvaluationTime(fixture.base)); own[supplementHostA] != digests[supplementHostA] {
		t.Fatalf("host A's digest %s here, %s in the probe: the digests are not the series'", own[supplementHostA], digests[supplementHostA])
	}
	// Host B's data for the Slot lands late: the Slot reads host A alone.
	held := execution.EvaluationTime(fixture.base + 60)
	late := true
	fixture.uqHosts = func(end int64) []string {
		if late {
			return []string{supplementHostA}
		}
		return []string{supplementHostA, supplementHostB}
	}
	if ended := runSlot(t, fixture, fixture.queryGroup, fixture.runner, held); ended != fullEnding {
		t.Fatalf("the Slot before the edit ended %q", ended)
	}
	hold := fixture.progress(ctx).LastCompletion.Contract.ReadHoldMillis

	installEditedStrategies(t, ctx, fixture.redisClient, 1725000600, thresholdTo(2))
	fixture.clock.Store((int64(held) + 30) * 1000)
	for round := 0; round < 2; round++ {
		if err := fixture.bundle.refreshAndReconcile(ctx, true); err != nil {
			t.Fatal(err)
		}
	}
	if closed, err := fixture.production.dependencies.Catalog.ReadFrozenSchedule(ctx, fixture.queryGroup, held); err != nil || closed.Segment.End == nil {
		t.Fatalf("the Slot's Segment after the edit = (%+v, %v), want it closed", closed.Segment, err)
	}

	late = false
	series := []execution.SeriesIdentityDigest{digests[supplementHostA], digests[supplementHostB]}
	sort.Slice(series, func(i, j int) bool { return series[i] < series[j] })
	client, err := accessuq.NewClient(fixture.cfg.PhaseTwo.Access.UQEndpoint, fixture.cfg.PhaseTwo.Access.QuerySource, http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	runtime := fixture.runner.(*settlingRuntime).phaseTwoQueryGroupRuntime.(*productionPhaseTwoQueryGroup)
	facts, err := runtime.Supplement(access.WithKeptRead(testwait.Context(t), recheckedRead{client: client}), held, hold,
		execution.SupplementScope{Series: series})
	if err != nil {
		t.Fatalf("Supplement() error = %v", err)
	}
	if facts.Admitted != 1 || facts.CrossedT != 1 || facts.ConfigDrift != 0 {
		t.Fatalf("supplement facts %+v, want host B admitted, host A crossed and nothing taken for drift", facts)
	}
	if written := seriesDigests(fixture, held); written[supplementHostB] != digests[supplementHostB] {
		t.Fatalf("the supplement wrote events for %v, want host B's", written)
	}
}

// A supplement written under a gap marker still open, then an edit (design
// 013 section 4.3, the negative): the old generation's guard does not
// follow to the new one, its key holds the generation. The supplemented
// series' Level is held GAPPED by that guard, not for holes its points show,
// so the carry skips it (skipped_guard_gapped) and it starts the new
// generation from nothing; the series whose window only has the read's own
// hole is carried with it. A window of three positions, at a threshold the
// data never crosses, so what decides is completeness alone.
func TestASupplementUnderAnOpenGuardIsNotCarriedAcrossAnEdit(t *testing.T) {
	previousWindow := cutoverStallTriggerWindow
	cutoverStallTriggerWindow = 3
	t.Cleanup(func() { cutoverStallTriggerWindow = previousWindow })
	// The digests the hosts' series go by, from a run where both report.
	probe := firingFixture(t)
	probe.uqHosts = func(int64) []string { return []string{supplementHostA, supplementHostB} }
	probeSlot := execution.EvaluationTime(probe.base + 60)
	if ended := runSlot(t, probe, probe.queryGroup, probe.runner, probeSlot); ended != fullEnding {
		t.Fatalf("the probe Slot ended %q", ended)
	}
	digests := seriesDigests(probe, probeSlot)
	if len(digests) != 2 {
		t.Fatalf("probe digests %v, want one per host", digests)
	}

	fixture := startCutoverFixtureWith(t, nil, observability.Discard(observability.ComponentRuntime), func(ctx context.Context, client *redis.Client, _ config.Config) {
		installEditedStrategies(t, ctx, client, 1725000000, thresholdTo(10))
	})
	ctx := context.Background()
	// The Slot's read comes back partial and without host B: the Plan's
	// marker opens GAPPED.
	held := execution.EvaluationTime(fixture.base + 60)
	hosts, partial := []string{supplementHostA}, true
	fixture.uqHosts = func(int64) []string { return hosts }
	fixture.uqPartial = func(int64) bool { return partial }
	if ended := runSlot(t, fixture, fixture.queryGroup, fixture.runner, held); ended != "COMPLETED_WITH_PARTIAL_GAP/QUERY_PARTIAL" {
		t.Fatalf("the partial Slot ended %q", ended)
	}
	partial = false
	marker, found := fixture.readPlanGapMarker(ctx)
	if scopes, _ := marker["scopes"].([]any); !found || len(scopes) != 1 || scopes[0].(map[string]any)["Status"] != "GAPPED" {
		t.Fatalf("the Plan's marker after the partial Slot = %v, want it open, GAPPED", marker)
	}

	// Host B's data lands; the supplement admits it at the Slot, under the
	// marker that still stands.
	hosts = []string{supplementHostA, supplementHostB}
	client, err := accessuq.NewClient(fixture.cfg.PhaseTwo.Access.UQEndpoint, fixture.cfg.PhaseTwo.Access.QuerySource, http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	runtime := fixture.runner.(*settlingRuntime).phaseTwoQueryGroupRuntime.(*productionPhaseTwoQueryGroup)
	facts, err := runtime.Supplement(access.WithKeptRead(testwait.Context(t), recheckedRead{client: client}), held,
		fixture.progress(ctx).LastCompletion.Contract.ReadHoldMillis, execution.SupplementScope{Series: []execution.SeriesIdentityDigest{digests[supplementHostB]}})
	if err != nil || facts.Admitted != 1 {
		t.Fatalf("Supplement() = (%+v, %v), want host B admitted", facts, err)
	}

	// A threshold edit, carried with the detection changed.
	installEditedStrategies(t, ctx, fixture.redisClient, 1725000600, thresholdTo(11))
	first := held + 60
	fixture.clock.Store((int64(first) - 1) * 1000)
	for round := 0; round < 2; round++ {
		if err := fixture.bundle.refreshAndReconcile(ctx, true); err != nil {
			t.Fatal(err)
		}
	}
	before := len(fixture.observed())
	runSlot(t, fixture, fixture.queryGroup, fixture.runner, first)
	series := map[string]int{}
	valid := map[string]uint32{}
	for _, observation := range fixture.observed()[before:] {
		if facts := observation.StateCarry; facts != nil && facts.Scope == "series" {
			series[facts.Result] += facts.Count
		}
		if coverage := observation.HistoryCoverage; coverage != nil {
			for _, window := range coverage.Windows {
				for host, digest := range digests {
					if string(window.Series) == string(digest) {
						valid[host] = window.Valid
					}
				}
			}
		}
	}
	if series["skipped_guard_gapped"] != 1 || series["carried_across_detect"] != 1 {
		t.Fatalf("series carried %v, want host B skipped for the guard and host A carried", series)
	}
	// Host B starts from its first point under the new generation; host A
	// keeps the point before the partial read, its hole carried as a hole.
	if valid[supplementHostB] != 1 || valid[supplementHostA] != 2 {
		t.Fatalf("valid positions %v, want host B 1 (started over) and host A 2 (carried with its hole)", valid)
	}
}
