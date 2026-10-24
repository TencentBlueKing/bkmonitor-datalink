// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lookback

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// namedQuery is query with a real physical query that asks for named
// outputs: A beside C, the legacy output detection reads.
func namedQuery(t *testing.T, queryGroup string, slot int64, step time.Duration, semantics string) Query {
	t.Helper()
	q := query(queryGroup, slot, step, semantics)
	facts, err := execution.BuildQueryPlanFacts(execution.QueryPlanFacts{
		Provider: execution.ProviderUQ, ProviderRouteRef: "uq-main", TenantID: "tenant", BusinessID: "2", SpaceScope: "bkcc__2",
		QueryList: []execution.QueryClause{{DataSource: "bkmonitor", TableID: "system.cpu", FieldName: "usage",
			ReferenceName: "a", Driver: "influxdb", TimeField: "time",
			TimeAggregation: execution.QueryFunction{Method: "avg", Window: "60s"}}},
		MetricMerge: "a", StepMillis: step.Milliseconds(), AlignmentMillis: step.Milliseconds(),
		DownSampleRange: execution.DownSampleNone, Timezone: "UTC", SourceSemantics: []string{semantics},
		NamedOutputs: &execution.NamedOutputs{LegacyRef: "C", Outputs: []execution.NamedOutput{{Ref: "A", Expression: "a"}, {Ref: "C", Expression: "a"}}},
		Normalization: execution.DatasetNormalizationSpec{DatasetContract: contract.DatasetContractV2{
			SchemaDigest: strings.Repeat("a", 64), NormalizationDigest: strings.Repeat("b", 64),
			IdentityFields: []string{"bk_target_ip"}, SourceTimeField: "_time", ReceivedTimeField: "_received_time"},
			SourceTimeUnit: execution.TimeUnitMillisecond, CanonicalSourceTimeUnit: execution.TimeUnitSecond,
			SeriesIdentityMode: execution.SeriesIdentityUQGroupKeysValuesV1, GroupKeyRule: execution.GroupKeyStripTableSuffixV1,
			ValueSelectionMode: execution.ValueSelectionResultOrFirstReferenceV1, CanonicalValueField: "value",
			ReceivedTimeMode: execution.ReceivedTimeProviderReceivedAt, Version: "uq-threshold-normalization-v1"}})
	if err != nil {
		t.Fatal(err)
	}
	window := q.Spec.LogicalWindow
	spec, err := execution.BuildPhysicalQuerySpec(execution.PhysicalQuerySpec{PlanFacts: facts, LogicalWindow: window,
		ProviderRange: window, AcceptedRange: window, RequiredColumns: []string{"value"}})
	if err != nil {
		t.Fatal(err)
	}
	q.Spec = spec
	return q
}

// lastAsked is the last query a lookback read asked for.
func (f *fixture) lastAsked() (execution.PhysicalQuerySpec, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.specs) == 0 {
		return execution.PhysicalQuerySpec{}, 0
	}
	return f.specs[len(f.specs)-1], len(f.specs)
}

// wantLegacyOutputOnly says a lookback read of a query with named outputs
// asked for its legacy output alone: the same query without them, a
// physical query of its own that validates.
func wantLegacyOutputOnly(t *testing.T, asked, formal execution.PhysicalQuerySpec) {
	t.Helper()
	if asked.PlanFacts.NamedOutputs != nil || asked.Digest == formal.Digest || asked.Validate() != nil ||
		asked.PlanFacts.MetricMerge != formal.PlanFacts.MetricMerge {
		t.Fatalf("a lookback read asked for %+v (outputs %+v), want the legacy output alone", asked.Digest, asked.PlanFacts.NamedOutputs)
	}
}

// A recheck of a Query Group with named outputs reads its legacy output
// alone, and is compared with the first read like any other: the series the
// first read delivered are the legacy output's, under the same identities.
func TestARecheckOfANamedOutputsGroupReadsItsLegacyOutputAlone(t *testing.T) {
	f := newFixture(t)
	slot := f.clock.now().Unix()
	formal := namedQuery(t, "qg", slot, minute, sourceLog)
	f.capture(formal, point(slot, "1"))
	stats := f.recheck(sourceLog, f.clock.now(), 0, minute, full(point(slot, "1")), RecheckCompared)
	asked, _ := f.lastAsked()
	wantLegacyOutputOnly(t, asked, formal.Spec)
	for fault, n := range stats.Faults {
		if n != 0 {
			t.Fatalf("fault %s counted %d", fault, n)
		}
	}
}

// A read-hold trial of a Query Group with named outputs reads its legacy
// output alone and is compared whole with the formal first read: equal
// series give equal evidence, with nothing matched by digest.
func TestAReadHoldTrialOfANamedOutputsGroupReadsItsLegacyOutputAlone(t *testing.T) {
	f := newFixture(t)
	observed := make(chan EarlierReadEvidence, 1)
	f.engine.options.OnEarlierRead = func(evidence EarlierReadEvidence) { observed <- evidence }
	held := heldQuery(f)
	q := namedQuery(t, "qg", int64(held.Contract.Slot.EvaluationTime), minute, sourceLog)
	q.Contract.ReadHoldMillis, q.ReadyAt = held.Contract.ReadHoldMillis, held.ReadyAt
	q.Spec.PlanFacts.QueryDelaySeconds = 60
	facts, err := execution.BuildQueryPlanFacts(func() execution.QueryPlanFacts {
		rebuilt := q.Spec.PlanFacts
		rebuilt.QueryRevision = ""
		return rebuilt
	}())
	if err != nil {
		t.Fatal(err)
	}
	q.Spec.PlanFacts, q.Spec.Digest = facts, ""
	if q.Spec, err = execution.BuildPhysicalQuerySpec(q.Spec); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 4; index++ {
		f.engine.Prepare(q)
	}
	trial := f.group("qg").prepared
	if trial == nil {
		t.Fatal("no trial reserved")
	}
	f.clock.set(trial.at)
	f.answers <- full(point(int64(q.Contract.Slot.EvaluationTime), "1"))
	f.engine.StepEarly(context.Background())
	f.waitFor(func(stats Stats) bool { return stats.PendingBytes == summaryEntryBytes })
	asked, calls := f.lastAsked()
	if calls != 1 {
		t.Fatalf("%d trial reads", calls)
	}
	wantLegacyOutputOnly(t, asked, q.Spec)
	f.clock.set(q.ReadyAt)
	read := f.engine.Begin(q)
	if read == nil || read.earlier != trial {
		t.Fatal("Begin did not continue the reserved trial")
	}
	read.Series(point(int64(q.Contract.Slot.EvaluationTime), "1"), 100)
	read.Complete(execution.ProviderCompletion{Completeness: execution.CompletenessFull}, nil)
	if evidence := <-observed; !evidence.Observed || !evidence.Equal {
		t.Fatalf("evidence %+v", evidence)
	}
}

// A Slot of a directed Query Group with named outputs is not read again
// and not supplemented: a supplement would evaluate its legacy output alone,
// and alert with none of the outputs. It is one physical query, so
// multi_query does not take it; it is counted on its own, named_outputs,
// both as an unobserved window and as an early read outside the mechanism -
// never a kept read that is absent.
func TestADirectedSlotWithNamedOutputsIsNotSupplemented(t *testing.T) {
	f, recorder := directedFixture(t, SupplementOutcome{Ran: true})
	slot := f.clock.now().Unix()
	_, before := f.lastAsked()
	read := f.engine.Begin(namedQuery(t, "qg", slot, minute, sourceLog))
	if read == nil || read.directed == nil {
		t.Fatal("the named Slot's first read was not kept for its directed read")
	}
	read.Series(point(slot, "1"), 100)
	read.Complete(execution.ProviderCompletion{Completeness: execution.CompletenessFull}, nil)
	f.clock.set(f.clock.now().Add(rungDelay(0, minute)))
	f.engine.Step(context.Background())
	stats := f.waitFor(func(stats Stats) bool { return stats.Sources[sourceLog].SupplementWindows[DirectedUnobserved] == 1 })
	source := stats.Sources[sourceLog]
	if source.SupplementUnobserved[UnobservedNamedOutputs] != 1 || source.SupplementUnobserved[UnobservedMultiQuery] != 0 ||
		source.SupplementWindows[DirectedKeptReadAbsent] != 0 || len(recorder.taken()) != 0 {
		t.Fatalf("windows %v unobserved %v jobs %d", source.SupplementWindows, source.SupplementUnobserved, len(recorder.taken()))
	}
	if source.EarlyReads[EarlyNamedOutputs] != 1 || source.EarlyReads[EarlyKeptReadAbsent] != 0 ||
		EarlyGroupOf(EarlyNamedOutputs) != EarlyGroupOutside {
		t.Fatalf("early reads %v", source.EarlyReads)
	}
	if _, after := f.lastAsked(); after != before {
		t.Fatalf("%d reads asked for a Slot that is not read again", after-before)
	}
}
