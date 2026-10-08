// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package access

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/lookback"
)

func logCountFacts(t *testing.T) execution.QueryPlanFacts {
	t.Helper()
	facts, err := execution.BuildQueryPlanFacts(execution.QueryPlanFacts{Provider: execution.ProviderUQ, ProviderRouteRef: "uq-main",
		TenantID: "tenant", BusinessID: "2", SpaceScope: "bkcc__2", SourceSemantics: []string{"bk_log_search/log"},
		QueryList: []execution.QueryClause{{DataSource: "bklog", TableID: "2_bklog.app", FieldName: "_index", ReferenceName: "a",
			Driver: "elasticsearch", TimeField: "dtEventTimeStamp", TimeAggregation: execution.QueryFunction{Method: "count_over_time", Position: 0, Window: "60s"}}},
		MetricMerge: "a", StepMillis: 60_000, AlignmentMillis: 60_000, Timezone: "UTC",
		Normalization: execution.DatasetNormalizationSpec{DatasetContract: contract.DatasetContractV2{SchemaDigest: strings.Repeat("a", 64), NormalizationDigest: strings.Repeat("b", 64), IdentityFields: []string{"host"}, SourceTimeField: "_time", ReceivedTimeField: "_received_time"},
			SourceTimeUnit: execution.TimeUnitMillisecond, CanonicalSourceTimeUnit: execution.TimeUnitSecond,
			SeriesIdentityMode: execution.SeriesIdentityUQGroupKeysValuesV1, GroupKeyRule: execution.GroupKeyStripTableSuffixV1,
			ValueSelectionMode: execution.ValueSelectionResultOrFirstReferenceV1, CanonicalValueField: "value", ReceivedTimeMode: execution.ReceivedTimeProviderReceivedAt, Version: "uq-threshold-normalization-v1"}})
	if err != nil {
		t.Fatal(err)
	}
	return facts
}

type lookbackRecheck struct {
	mu    sync.Mutex
	hosts map[string]string
	specs []execution.PhysicalQuerySpec
}

// read answers a recheck with one series per host, each at the value given.
func (recheck *lookbackRecheck) read(ctx context.Context, spec execution.PhysicalQuerySpec, sink execution.ProviderSeriesSink) (execution.ProviderCompletion, error) {
	recheck.mu.Lock()
	recheck.specs = append(recheck.specs, spec)
	hosts := recheck.hosts
	recheck.mu.Unlock()
	for host, value := range hosts {
		fields := []contract.DimensionFieldV2{{Name: "host", Value: json.RawMessage(`"` + host + `"`)}}
		dimension, _ := contract.DeriveDimensionIdentityDigestV2("tenant", "2", fields)
		records := []contract.CanonicalRecordV2{{RecordID: host, SourceTime: 1_700_124_000 - 60,
			DimensionIdentity: contract.DimensionIdentityV2{Fields: fields, Digest: dimension},
			Values:            map[string]json.RawMessage{"value": json.RawMessage(value)},
			Dimensions:        map[string]json.RawMessage{"host": json.RawMessage(`"` + host + `"`)}}}
		_ = sink.ConsumeProviderSeries(ctx, execution.ProviderSeriesBatch{PhysicalQuery: spec.Digest, Dataset: execution.NewDataset(records)})
	}
	return execution.ProviderCompletion{Completeness: execution.CompletenessFull}, nil
}

// A first read is summarized at the layer a recheck reads: every series the
// provider delivered, the out-of-target one included. The recheck then finds
// both series' values changed in their bucket - not a point added, which is
// what a summary of the in-target series alone would have read.
func TestAFirstReadIsSummarizedBeforeTheTargetFilter(t *testing.T) {
	contractRef, frozen := frozenExecution(t)
	facts := logCountFacts(t)
	ref := execution.LogicalQueryRef(facts.QueryRevision)
	frozen.Requirements[0].LogicalQueryRef = ref
	frozen.QueryFacts = map[execution.LogicalQueryRef]execution.QueryPlanFacts{ref: facts}
	frozen.DuePlans[0].CompiledPlan = compilePlanForStrategy(t, "1001", hostScopeContract("192.0.2.10|0"))
	contractRef.QueryRevision = facts.QueryRevision
	contractRef = bindFrozenDueDigest(t, contractRef, frozen)

	now := time.Unix(1_700_124_010, 0)
	var clock sync.Mutex
	recheck := &lookbackRecheck{hosts: map[string]string{"192.0.2.10": "70", "192.0.2.99": "90"}}
	engine, err := lookback.New(lookback.Options{Recheck: recheck.read, UnspreadFirstSamples: true,
		Now:    func() time.Time { clock.Lock(); defer clock.Unlock(); return now },
		Permit: func() (func(), <-chan struct{}, string) { return func() {}, nil, "" }, Owns: func(execution.QueryGroupIdentity) bool { return true },
		Owned: func() int { return 1 }})
	if err != nil {
		t.Fatal(err)
	}
	source, err := NewSource(staticFrozenPlan{plan: frozen}, &hostStreamingProvider{hosts: []string{"192.0.2.10", "192.0.2.99"}},
		&recordingQueryPermits{}, Config{MinReadyDelay: time.Second, Admission: scopedChain(), Lookback: engine})
	if err != nil {
		t.Fatal(err)
	}
	source.now = func() time.Time { return time.UnixMilli(2_000_000_000_000) }
	source.wait = func(context.Context, time.Duration) error { return nil }
	consumer := &recordingConsumer{}
	if _, err := source.Execute(context.Background(), execution.QueryExecutionRequest{
		Contract: contractRef, Operation: execution.OperationNormal, AttemptNo: 1}, consumer); err != nil {
		t.Fatal(err)
	}
	if len(consumer.batches) != 1 {
		t.Fatalf("the pipeline got %d batches, want only the in-target series", len(consumer.batches))
	}
	stats := engine.Stats()
	if stats.Sources["bk_log_search/log"].Samples[lookback.OutcomeCaptured] != 1 || stats.Pending != 1 {
		t.Fatalf("samples %v pending %d", stats.Sources["bk_log_search/log"].Samples, stats.Pending)
	}

	clock.Lock()
	now = now.Add(time.Duration(lookback.RungSteps[0] * float64(time.Duration(facts.StepMillis)*time.Millisecond)))
	clock.Unlock()
	engine.Step(context.Background())
	first := lookback.RungNames[0]
	deadline := time.Now().Add(5 * time.Second)
	for engine.Stats().Sources["bk_log_search/log"].Rechecks[first][lookback.RecheckCompared] == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("no recheck: %v", engine.Stats().Sources["bk_log_search/log"].Rechecks[first])
		}
		time.Sleep(time.Millisecond)
	}
	changes := engine.Stats().Sources["bk_log_search/log"].Changes[first]
	if changes[lookback.ChangeValuesChanged] != 1 || changes[lookback.ChangePointsAdded] != 0 {
		t.Fatalf("changes %v, want the one bucket's values changed, both series summarized", changes)
	}
	recheck.mu.Lock()
	defer recheck.mu.Unlock()
	if len(recheck.specs) != 1 || recheck.specs[0].Digest == "" || recheck.specs[0].PlanFacts.QueryRevision != facts.QueryRevision {
		t.Fatalf("recheck read %+v, want the frozen physical query", recheck.specs)
	}
}

// Every source is measured, under the label the catalog gives its Query
// Group: the fixture's query names no source, as the compiler writes plain
// time series, and is counted as bk_monitor/time_series - not as other.
func TestAQueryOfAnySourceIsTakenAsItsQueryGroupsSample(t *testing.T) {
	contractRef, frozen := frozenExecution(t)
	engine, err := lookback.New(lookback.Options{UnspreadFirstSamples: true,
		Recheck: func(context.Context, execution.PhysicalQuerySpec, execution.ProviderSeriesSink) (execution.ProviderCompletion, error) {
			return execution.ProviderCompletion{}, nil
		},
		Permit: func() (func(), <-chan struct{}, string) { return func() {}, nil, "" }, Owns: func(execution.QueryGroupIdentity) bool { return true },
		Owned: func() int { return 1 }})
	if err != nil {
		t.Fatal(err)
	}
	source, err := NewSource(staticFrozenPlan{plan: frozen}, &fakeProvider{}, &recordingQueryPermits{},
		Config{MinReadyDelay: time.Second, Lookback: engine})
	if err != nil {
		t.Fatal(err)
	}
	source.now = func() time.Time { return time.UnixMilli(2_000_000_000_000) }
	source.wait = func(context.Context, time.Duration) error { return nil }
	if _, err := source.Execute(context.Background(), execution.QueryExecutionRequest{
		Contract: contractRef, Operation: execution.OperationNormal, AttemptNo: 1}, &recordingConsumer{}); err != nil {
		t.Fatal(err)
	}
	stats := engine.Stats()
	if stats.Sources["bk_monitor/time_series"].Samples[lookback.OutcomeCaptured] != 1 || stats.Coverage.Covered != 1 ||
		stats.Sources[lookback.SourceOther].FirstReads != 0 {
		t.Fatalf("samples %v, other %d, coverage %+v; want the Query Group sampled as bk_monitor/time_series",
			stats.Sources["bk_monitor/time_series"].Samples, stats.Sources[lookback.SourceOther].FirstReads, stats.Coverage)
	}
}
