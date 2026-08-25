// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package v2

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

func TestAdapterIsolatesPlanLevelAndRecordIssues(t *testing.T) {
	t.Parallel()

	t.Run("plan", func(t *testing.T) {
		t.Parallel()
		envelope := validEnvelope(t, 1)
		invalid := validPlan("1001", 1)
		invalid.StrategyIR.Levels = append(invalid.StrategyIR.Levels, invalid.StrategyIR.Levels[0])
		envelope.PlanSet.EvaluationPlans = []contract.EvaluationPlanV2{invalid, validPlan("1002", 1)}
		envelope.PlanSet.PlanCount = 2
		ranges := []contract.SelectorRangeV2{{Start: 0, End: 1}}
		envelope.Selectors = []contract.PlanSelectorV2{
			{PlanOrdinal: 0, Selector: contract.SelectorV2{Kind: contract.SelectorKindRanges, Ranges: &ranges}},
			{PlanOrdinal: 1, Selector: contract.SelectorV2{Kind: contract.SelectorKindRanges, Ranges: &ranges}},
		}

		result, err := New(readerLimits()).Decode(context.Background(), encodeEnvelope(t, envelope))
		if err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		plans := result.Input.PlanViews()
		if len(plans) != 1 || plans[0].PlanID() != "1002" {
			t.Fatalf("PlanViews() = %#v, want only plan 1002", plans)
		}
		selections := result.Input.PlanSelections()
		if len(selections) != 2 || selections[0].Evaluable() || !selections[1].Evaluable() {
			t.Fatalf("PlanSelections() = %#v, want terminal plan 1001 and evaluable plan 1002", selections)
		}
		if selections[0].SelectedCount() != 1 {
			t.Fatalf("terminal plan selected = %d, want 1 for Receipt conservation", selections[0].SelectedCount())
		}
		topSelected := selections[0].SelectedCount() + selections[1].SelectedCount()
		processed := len(selectedRecordIDs(t, plans[0]))
		terminal := selections[0].SelectedCount()
		if topSelected != processed+terminal {
			t.Fatalf("Receipt counts do not conserve: selected=%d processed=%d terminal=%d", topSelected, processed, terminal)
		}
		assertTerminal(t, result.Terminals, ScopePlan, contract.ReasonPlanDuplicateLevelID, 0)
	})

	t.Run("level", func(t *testing.T) {
		t.Parallel()
		envelope := validEnvelope(t, 1)
		plan := validPlan("1001", 1)
		plan.StrategyIR.Levels[0].Definition.Priority = 0
		plan.StrategyIR.Levels = append(plan.StrategyIR.Levels, validPlan("1001", 5).StrategyIR.Levels[0])
		envelope.PlanSet.EvaluationPlans = []contract.EvaluationPlanV2{plan}

		result, err := New(readerLimits()).Decode(context.Background(), encodeEnvelope(t, envelope))
		if err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		levels := result.Input.PlanViews()[0].Strategy().Snapshot().Levels
		if len(levels) != 1 || levels[0].Definition.LevelID != 5 {
			t.Fatalf("valid Levels = %#v, want only level 5", levels)
		}
		assertTerminal(t, result.Terminals, ScopeLevel, contract.ReasonLevelInvalid, 0)
	})

	t.Run("record", func(t *testing.T) {
		t.Parallel()
		envelope := validEnvelope(t, 2)
		envelope.Records[0].RecordID = strings.Repeat("0", 64)

		result, err := New(readerLimits()).Decode(context.Background(), encodeEnvelope(t, envelope))
		if err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		batch := result.Input.RecordBatch()
		if batch.Len() != 2 || batch.ValidLen() != 1 {
			t.Fatalf("RecordBatch = (%d, %d), want stable 2 slots and 1 valid", batch.Len(), batch.ValidLen())
		}
		if _, ok := batch.Record(0); ok {
			t.Fatal("invalid record ordinal 0 remained visible")
		}
		if got := selectedRecordIDs(t, result.Input.PlanViews()[0]); len(got) != 1 || got[0] != envelope.Records[1].RecordID {
			t.Fatalf("selected valid records = %v, want only ordinal 1", got)
		}
		assertTerminal(t, result.Terminals, ScopeRecord, contract.ReasonRecordIdentityConflict, 0)
		items := result.Terminals.Items()
		*items[0].RecordOrdinal = 99
		if got := *result.Terminals.Items()[0].RecordOrdinal; got != 0 {
			t.Fatalf("TerminalSet leaked mutable ordinal backing: %d", got)
		}
	})
}

func TestAdapterDoesNotExposeUntrustedPlanIdentityInSiblingTerminals(t *testing.T) {
	t.Parallel()

	envelope := validEnvelope(t, 1)
	plan := envelope.PlanSet.EvaluationPlans[0]
	plan.PlanID = "invalid"
	plan.StrategyIR.Levels = append(plan.StrategyIR.Levels, plan.StrategyIR.Levels[0])
	envelope.PlanSet.EvaluationPlans[0] = plan

	result, err := New(readerLimits()).Decode(context.Background(), encodeEnvelope(t, envelope))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if result.Terminals.Len() < 2 {
		t.Fatalf("terminals = %#v, want plan identity and duplicate-level issues", result.Terminals.Items())
	}
	for _, terminal := range result.Terminals.Items() {
		if terminal.PlanID != "" {
			t.Fatalf("terminal leaked untrusted plan_id %q: %#v", terminal.PlanID, terminal)
		}
	}
}

func TestAdapterTerminalizesEveryValidationBudgetTailObject(t *testing.T) {
	t.Parallel()

	t.Run("plan tail also invalidates record tail", func(t *testing.T) {
		t.Parallel()
		envelope := validEnvelope(t, 1)
		invalid := validPlan("1001", 1)
		invalid.StrategyIR.Levels = append(invalid.StrategyIR.Levels, invalid.StrategyIR.Levels[0])
		envelope.PlanSet.EvaluationPlans = []contract.EvaluationPlanV2{invalid, validPlan("1002", 1)}
		envelope.PlanSet.PlanCount = 2
		ranges := []contract.SelectorRangeV2{{Start: 0, End: 1}}
		envelope.Selectors = []contract.PlanSelectorV2{
			{PlanOrdinal: 0, Selector: contract.SelectorV2{Kind: contract.SelectorKindRanges, Ranges: &ranges}},
			{PlanOrdinal: 1, Selector: contract.SelectorV2{Kind: contract.SelectorKindRanges, Ranges: &ranges}},
		}
		limits := readerLimits()
		limits.MaxValidationIssues = 1

		result, err := New(limits).Decode(context.Background(), encodeEnvelope(t, envelope))
		if err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		if len(result.Input.PlanViews()) != 0 || result.Input.RecordBatch().ValidLen() != 0 {
			t.Fatalf("unverified tail remained legal: plans=%d valid_records=%d", len(result.Input.PlanViews()), result.Input.RecordBatch().ValidLen())
		}
		if got := result.Terminals.Len(); got != 1 {
			t.Fatalf("terminal count = %d, want one bounded tail terminal", got)
		}
		terminal := result.Terminals.Items()[0]
		if terminal.ReasonCode != contract.ReasonValidationBudgetExceeded || terminal.PlanFromOrdinal == nil ||
			*terminal.PlanFromOrdinal != 0 || terminal.RecordFromOrdinal == nil || *terminal.RecordFromOrdinal != 0 {
			t.Fatalf("tail terminal = %#v, want plans[0:] and records[0:]", terminal)
		}
	})

	t.Run("record tail", func(t *testing.T) {
		t.Parallel()
		envelope := validEnvelope(t, 3)
		for index := range envelope.Records {
			envelope.Records[index].RecordID = strings.Repeat(string(rune('a'+index)), 64)
		}
		limits := readerLimits()
		limits.MaxValidationIssues = 2

		result, err := New(limits).Decode(context.Background(), encodeEnvelope(t, envelope))
		if err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		if result.Input.RecordBatch().ValidLen() != 0 {
			t.Fatalf("ValidLen() = %d, want every record invalid", result.Input.RecordBatch().ValidLen())
		}
		if got := result.Terminals.Len(); got != 2 {
			t.Fatalf("terminal count = %d, want one located issue + one bounded tail", got)
		}
		tail := result.Terminals.Items()[1]
		if tail.RecordFromOrdinal == nil || *tail.RecordFromOrdinal != 1 {
			t.Fatalf("record tail terminal = %#v, want records[1:]", tail)
		}
	})

	t.Run("empty record tail", func(t *testing.T) {
		t.Parallel()
		envelope := validEnvelope(t, 0)
		invalid := envelope.PlanSet.EvaluationPlans[0]
		invalid.StrategyIR.Levels = append(invalid.StrategyIR.Levels, invalid.StrategyIR.Levels[0])
		envelope.PlanSet.EvaluationPlans[0] = invalid
		limits := readerLimits()
		limits.MaxValidationIssues = 1

		result, err := New(limits).Decode(context.Background(), encodeEnvelope(t, envelope))
		if err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		if result.Input.RecordBatch().Len() != 0 || result.Terminals.Len() != 1 {
			t.Fatalf("empty tail = records %d, terminals %d; want 0 and 1", result.Input.RecordBatch().Len(), result.Terminals.Len())
		}
	})

	t.Run("large record tail stays bounded", func(t *testing.T) {
		t.Parallel()
		envelope := validEnvelope(t, 100)
		for index := range envelope.Records {
			envelope.Records[index].RecordID = strings.Repeat(string(rune('a'+index%26)), 64)
		}
		limits := readerLimits()
		limits.MaxValidationIssues = 2

		result, err := New(limits).Decode(context.Background(), encodeEnvelope(t, envelope))
		if err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		if result.Input.RecordBatch().ValidLen() != 0 || result.Terminals.Len() != 2 {
			t.Fatalf("large tail = valid %d, terminals %d; want 0 and 2", result.Input.RecordBatch().ValidLen(), result.Terminals.Len())
		}
		tail := result.Terminals.Items()[1]
		if tail.RecordFromOrdinal == nil || *tail.RecordFromOrdinal != 1 {
			t.Fatalf("large tail terminal = %#v, want records[1:]", tail)
		}
		payload := encodeEnvelope(t, envelope)
		framed, issues, readErr := contract.ReadExecutionEnvelopeV2(payload, limits)
		if readErr != nil {
			t.Fatalf("ReadExecutionEnvelopeV2() error = %v", readErr)
		}
		isolation, isolateErr := isolateValidationIssues(&framed.Envelope, issues)
		if isolateErr != nil {
			t.Fatalf("isolateValidationIssues() error = %v", isolateErr)
		}
		if len(isolation.invalidRecords) != 1 || isolation.invalidRecordFrom == nil || *isolation.invalidRecordFrom != 1 {
			t.Fatalf("tail storage = map %d, range %#v; want O(issue budget) map 1 + range from 1", len(isolation.invalidRecords), isolation.invalidRecordFrom)
		}
	})
}

func assertTerminal(t *testing.T, terminals TerminalSet, scope TerminalScope, reason string, ordinal uint32) {
	t.Helper()
	for _, terminal := range terminals.Items() {
		if terminal.Scope != scope || terminal.ReasonCode != reason {
			continue
		}
		switch scope {
		case ScopePlan, ScopeLevel:
			if terminal.PlanOrdinal != nil && *terminal.PlanOrdinal == ordinal {
				return
			}
		case ScopeRecord:
			if terminal.RecordOrdinal != nil && *terminal.RecordOrdinal == ordinal {
				return
			}
		}
	}
	t.Fatalf("terminals = %#v, want %s/%s ordinal %d", terminals.Items(), scope, reason, ordinal)
}

func TestAdapterSharesOneRecordBatchAcrossPlanViews(t *testing.T) {
	t.Parallel()

	envelope := validEnvelope(t, 2)
	envelope.PlanSet.EvaluationPlans = []contract.EvaluationPlanV2{
		validPlan("1001", 5),
		validPlan("1002", 1),
	}
	envelope.PlanSet.PlanCount = 2
	firstRanges := []contract.SelectorRangeV2{{Start: 0, End: 2}}
	secondRanges := []contract.SelectorRangeV2{{Start: 1, End: 2}}
	envelope.Selectors = []contract.PlanSelectorV2{
		{PlanOrdinal: 0, Selector: contract.SelectorV2{Kind: contract.SelectorKindRanges, Ranges: &firstRanges}},
		{PlanOrdinal: 1, Selector: contract.SelectorV2{Kind: contract.SelectorKindRanges, Ranges: &secondRanges}},
	}

	result, err := New(readerLimits()).Decode(context.Background(), encodeEnvelope(t, envelope))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if result.Rejected || result.Input == nil || result.Terminals.Len() != 0 {
		t.Fatalf("Decode() = %#v, want accepted input without terminals", result)
	}
	if result.Input.Mode() != ModeQueryGroupV2 {
		t.Fatalf("Mode() = %q, want %q", result.Input.Mode(), ModeQueryGroupV2)
	}
	if result.Input.Execution().Completeness != contract.QueryCompletenessFull {
		t.Fatalf("Completeness = %q, want FULL", result.Input.Execution().Completeness)
	}
	if result.Input.Execution().PlanSetDigest != envelope.PlanSet.PlanSetDigest || result.Input.Execution().PayloadDigest != envelope.PayloadDigest {
		t.Fatalf("receipt digests were not preserved in execution metadata")
	}
	batch := result.Input.RecordBatch()
	if batch.Len() != 2 || batch.ValidLen() != 2 {
		t.Fatalf("RecordBatch = (%d, %d), want (2, 2)", batch.Len(), batch.ValidLen())
	}
	plans := result.Input.PlanViews()
	if len(plans) != 2 {
		t.Fatalf("len(PlanViews()) = %d, want 2", len(plans))
	}
	if plans[0].batch != batch || plans[1].batch != batch {
		t.Fatal("PlanViews do not share the EvaluationInput RecordBatch")
	}
	if plans[0].SelectedCount() != 2 || plans[1].SelectedCount() != 1 {
		t.Fatalf("SelectedCount = (%d, %d), want (2, 1)", plans[0].SelectedCount(), plans[1].SelectedCount())
	}
	if got := selectedRecordIDs(t, plans[1]); len(got) != 1 || got[0] != envelope.Records[1].RecordID {
		t.Fatalf("second Plan records = %v, want record ordinal 1", got)
	}
	strategy := plans[0].Strategy().Snapshot()
	if len(strategy.Levels) != 1 || strategy.Levels[0].Definition.LevelID != 5 {
		t.Fatalf("dynamic Level snapshot = %#v, want level_id=5", strategy.Levels)
	}
	record, ok := batch.Record(0)
	if !ok {
		t.Fatal("Record(0) is invalid")
	}
	value, ok := record.Value("value")
	if !ok {
		t.Fatal("Record(0).Value(value) is missing")
	}
	value[0] = '9'
	again, _ := record.Value("value")
	if string(again) != "50.1" {
		t.Fatalf("RecordView leaked mutable backing: %s", again)
	}
	dimensions := record.Dimensions()
	dimensions["host"][0] = 'x'
	againDimension, _ := record.Dimension("host")
	if string(againDimension) != `"127.0.0.1"` {
		t.Fatalf("RecordView leaked mutable dimensions backing: %s", againDimension)
	}
}

func TestAdapterKeepsBitmapAsAReadOnlyIndexView(t *testing.T) {
	t.Parallel()

	envelope := validEnvelope(t, 10)
	envelope.Selectors[0].Selector = contract.SelectorV2{Kind: contract.SelectorKindBitmap, BitmapB64: "gQM="}
	result, err := New(readerLimits()).Decode(context.Background(), encodeEnvelope(t, envelope))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	got := selectedRecordIDs(t, result.Input.PlanViews()[0])
	want := []string{envelope.Records[0].RecordID, envelope.Records[7].RecordID, envelope.Records[8].RecordID, envelope.Records[9].RecordID}
	if len(got) != len(want) {
		t.Fatalf("bitmap selected %d records, want %d", len(got), len(want))
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("bitmap record[%d] = %s, want %s", index, got[index], want[index])
		}
	}
}

func TestPlanViewExposesSharedInvalidRecordMembershipWithoutCopying(t *testing.T) {
	t.Parallel()

	envelope := validEnvelope(t, 2)
	envelope.Records[0].RecordID = strings.Repeat("0", 64)
	envelope.PlanSet.EvaluationPlans = []contract.EvaluationPlanV2{validPlan("1001", 1), validPlan("1002", 1)}
	envelope.PlanSet.PlanCount = 2
	all := []contract.SelectorRangeV2{{Start: 0, End: 2}}
	badOnly := []contract.SelectorRangeV2{{Start: 0, End: 1}}
	envelope.Selectors = []contract.PlanSelectorV2{
		{PlanOrdinal: 0, Selector: contract.SelectorV2{Kind: contract.SelectorKindRanges, Ranges: &all}},
		{PlanOrdinal: 1, Selector: contract.SelectorV2{Kind: contract.SelectorKindRanges, Ranges: &badOnly}},
	}

	result, err := New(readerLimits()).Decode(context.Background(), encodeEnvelope(t, envelope))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if result.Terminals.Len() != 1 {
		t.Fatalf("dataset terminals = %d, want one shared bad-record terminal", result.Terminals.Len())
	}
	plans := result.Input.PlanViews()
	assertSelectedSlotConservation(t, plans[0], 1, 1)
	assertSelectedSlotConservation(t, plans[1], 0, 1)
}

func assertSelectedSlotConservation(t *testing.T, plan PlanView, wantValid, wantTerminal int) {
	t.Helper()
	valid, terminal := 0, 0
	err := plan.ForEachSelectedSlot(func(ordinal uint32, record RecordView, ok bool) error {
		if ok {
			valid++
			if record.RecordID() == "" {
				t.Fatalf("valid selected slot %d has no record", ordinal)
			}
		} else {
			terminal++
			if record.RecordID() != "" {
				t.Fatalf("invalid selected slot %d exposed record data", ordinal)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ForEachSelectedSlot() error = %v", err)
	}
	if valid != wantValid || terminal != wantTerminal || plan.SelectedCount() != valid+terminal {
		t.Fatalf("plan %s counts = selected %d, valid %d, terminal %d; want valid %d terminal %d", plan.PlanID(), plan.SelectedCount(), valid, terminal, wantValid, wantTerminal)
	}
}

func TestAdapterPreservesQueryCompletenessWithoutInferringBusinessState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		result      contract.QueryResultV2
		recordCount int
		wantRoute   ProcessingRoute
	}{
		{name: "full empty", result: contract.QueryResultV2{Completeness: contract.QueryCompletenessFull}, wantRoute: RouteFullPipeline},
		{name: "partial data", result: contract.QueryResultV2{Completeness: contract.QueryCompletenessPartial, ReasonCode: contract.ReasonQueryPartial}, recordCount: 1, wantRoute: RouteDetectOnly},
		{name: "partial empty", result: contract.QueryResultV2{Completeness: contract.QueryCompletenessPartial, ReasonCode: contract.ReasonQueryPartial}, wantRoute: RouteDetectOnly},
		{name: "unavailable", result: contract.QueryResultV2{Completeness: contract.QueryCompletenessUnavailable, ReasonCode: contract.ReasonQueryUnavailable}, wantRoute: RouteNoEvaluation},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			envelope := validEnvelope(t, test.recordCount)
			envelope.QueryResult = test.result

			decoded, err := New(readerLimits()).Decode(context.Background(), encodeEnvelope(t, envelope))
			if err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			if decoded.Rejected || decoded.Input == nil {
				t.Fatalf("Decode() = %#v, want accepted input", decoded)
			}
			execution := decoded.Input.Execution()
			if execution.Completeness != test.result.Completeness || execution.QueryResultReason != test.result.ReasonCode {
				t.Fatalf("query result = (%q, %q), want (%q, %q)", execution.Completeness, execution.QueryResultReason, test.result.Completeness, test.result.ReasonCode)
			}
			if decoded.Input.RecordBatch().Len() != test.recordCount {
				t.Fatalf("record count = %d, want %d", decoded.Input.RecordBatch().Len(), test.recordCount)
			}
			if decoded.Input.ProcessingRoute() != test.wantRoute {
				t.Fatalf("ProcessingRoute() = %q, want %q", decoded.Input.ProcessingRoute(), test.wantRoute)
			}
		})
	}
}

func TestAdapterTurnsFramingFailureIntoDeterministicMessageRejection(t *testing.T) {
	t.Parallel()

	envelope := validEnvelope(t, 1)
	_ = encodeEnvelope(t, envelope)
	envelope.PayloadDigest = strings.Repeat("0", 64)
	payload, err := contract.CanonicalJSONV2(envelope)
	if err != nil {
		t.Fatalf("CanonicalJSONV2() error = %v", err)
	}

	result, err := New(readerLimits()).Decode(context.Background(), payload)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if !result.Rejected || result.Input != nil || result.Terminals.Len() != 1 {
		t.Fatalf("Decode() = %#v, want rejected message without partial input", result)
	}
	terminal := result.Terminals.Items()[0]
	if terminal.Scope != ScopeMessage || terminal.ReasonCode != contract.ReasonPayloadDigestMismatch {
		t.Fatalf("terminal = %#v, want message/PAYLOAD_DIGEST_MISMATCH", terminal)
	}
}

func TestAdapterTreatsInvalidReaderLimitsAsConfigurationFailure(t *testing.T) {
	t.Parallel()

	limits := readerLimits()
	limits.MaxValidationIssues = 0
	adapter := New(limits)
	if adapter.Validate() == nil {
		t.Fatal("Validate() error = nil, want invalid Reader limits")
	}
	result, err := adapter.Decode(context.Background(), encodeEnvelope(t, validEnvelope(t, 1)))
	if err == nil {
		t.Fatal("Decode() error = nil, want configuration failure")
	}
	if result.Rejected || result.Terminals.Len() != 0 {
		t.Fatalf("Decode() = %#v, invalid local configuration must not terminalize input", result)
	}
}

func selectedRecordIDs(t *testing.T, plan PlanView) []string {
	t.Helper()
	ids := make([]string, 0, plan.SelectedCount())
	if err := plan.ForEachRecord(func(record RecordView) error {
		ids = append(ids, record.RecordID())
		return nil
	}); err != nil {
		t.Fatalf("ForEachRecord() error = %v", err)
	}
	return ids
}

func validEnvelope(t testing.TB, recordCount int) *contract.ExecutionEnvelopeV2 {
	t.Helper()

	const firstSourceTime int64 = 1_725_000_000
	records := make([]contract.CanonicalRecordV2, 0, recordCount)
	for index := 0; index < recordCount; index++ {
		records = append(records, validRecord(t, firstSourceTime+int64(index*60), json.RawMessage(`50.1`)))
	}
	untilTime := firstSourceTime + int64(max(recordCount, 1)*60)
	ranges := make([]contract.SelectorRangeV2, 0, 1)
	if recordCount > 0 {
		ranges = append(ranges, contract.SelectorRangeV2{Start: 0, End: uint32(recordCount)})
	}
	return &contract.ExecutionEnvelopeV2{
		Schema:           contract.Schema{Name: contract.ExecutionEnvelopeSchemaV2, Major: 2, Minor: 0},
		RequiredFeatures: []string{},
		ExecutionID:      "execution-1",
		MessageID:        "message-1",
		TenantID:         "default",
		QueryGroup: contract.QueryGroupV2{
			Key: "query-group-1", QueryMD5: "query-md5-1", QueryRevision: "query-r1", EvaluationTime: untilTime,
		},
		SourceWindow: contract.SourceWindowV2{FromTime: firstSourceTime - 300, UntilTime: untilTime},
		QueryResult:  contract.QueryResultV2{Completeness: contract.QueryCompletenessFull},
		DatasetContract: contract.DatasetContractV2{
			SchemaDigest:        "1111111111111111111111111111111111111111111111111111111111111111",
			NormalizationDigest: "2222222222222222222222222222222222222222222222222222222222222222",
			IdentityFields:      []string{"host"}, SourceTimeField: "time", ReceivedTimeField: "received_time",
		},
		PlanSet: contract.PlanSetV2{PlanCount: 1, EvaluationPlans: []contract.EvaluationPlanV2{validPlan("1001", 5)}},
		Selectors: []contract.PlanSelectorV2{{
			PlanOrdinal: 0, Selector: contract.SelectorV2{Kind: contract.SelectorKindRanges, Ranges: &ranges},
		}},
		Records: records,
	}
}

func validPlan(strategyID string, levelID uint32) contract.EvaluationPlanV2 {
	ref := contract.StrategyRefV2{TenantID: "default", StrategyID: strategyID, Revision: "strategy-r1"}
	projection := contract.InputProjectionV2{
		ValueFields: []string{"value"}, DimensionFields: []string{"host"}, BusinessIdentityField: "bk_biz_id",
		MultiValueAlignment: "SINGLE_VALUE", DataUnit: "percent", MissingValuePolicy: contract.MissingValuePolicyRequired,
	}
	strategy := contract.StrategyIRV2{
		Schema: contract.Schema{Name: contract.StrategyIRSchemaV2, Major: 2, Minor: 0}, RequiredFeatures: []string{},
		StrategyRef: ref,
		ExecutionSemantics: contract.ExecutionSemanticsV2{
			EvaluationScope: contract.EvaluationScopeSeries, QueryWindow: 300, AggregationInterval: 60,
			EvaluationInterval: 60, LatenessTolerance: 120,
		},
		InputProjection: projection,
		Levels: []contract.LevelIRV2{{
			Definition: contract.LevelDefinitionV2{LevelID: levelID, LevelCode: "level", Priority: 1},
			Connector:  contract.LevelConnectorAND,
			DetectPlan: contract.DetectPlanV2{Algorithms: []contract.AlgorithmIRV2{{
				Type: "THRESHOLD", Version: 1, Config: json.RawMessage(`{"method":"gt","threshold":40}`),
			}}},
			TriggerPlan:  contract.TypedPlanV1{Type: "N_OF_M", Version: 1, Config: json.RawMessage(`{"m":1,"n":1}`)},
			RecoveryPlan: contract.TypedPlanV1{Type: "N_OF_M", Version: 1, Config: json.RawMessage(`{"m":1,"n":1}`)},
		}},
	}
	return contract.EvaluationPlanV2{PlanID: strategyID, StrategyRef: ref, InputProjection: projection, StrategyIR: strategy}
}

func validRecord(t testing.TB, sourceTime int64, value json.RawMessage) contract.CanonicalRecordV2 {
	t.Helper()
	fields := []contract.DimensionFieldV2{{Name: "host", Value: json.RawMessage(`"127.0.0.1"`)}}
	dimensionDigest, err := contract.DeriveDimensionIdentityDigestV2("default", "2", fields)
	if err != nil {
		t.Fatalf("DeriveDimensionIdentityDigestV2() error = %v", err)
	}
	recordID, err := contract.DeriveRecordIDV2(dimensionDigest, sourceTime)
	if err != nil {
		t.Fatalf("DeriveRecordIDV2() error = %v", err)
	}
	return contract.CanonicalRecordV2{
		RecordID: recordID, SourceTime: sourceTime, BusinessID: "2",
		DimensionIdentity: contract.DimensionIdentityV2{Fields: fields, Digest: dimensionDigest},
		Values:            map[string]json.RawMessage{"value": value},
		Dimensions:        map[string]json.RawMessage{"host": json.RawMessage(`"127.0.0.1"`)},
		ReceivedTime:      sourceTime + 1,
	}
}

func encodeEnvelope(t testing.TB, envelope *contract.ExecutionEnvelopeV2) []byte {
	t.Helper()
	planDigest, err := contract.DerivePlanSetDigestV2(envelope.PlanSet)
	if err != nil {
		t.Fatalf("DerivePlanSetDigestV2() error = %v", err)
	}
	envelope.PlanSet.PlanSetDigest = planDigest
	payloadDigest, err := contract.DeriveExecutionEnvelopePayloadDigestV2(*envelope)
	if err != nil {
		t.Fatalf("DeriveExecutionEnvelopePayloadDigestV2() error = %v", err)
	}
	envelope.PayloadDigest = payloadDigest
	payload, err := contract.CanonicalJSONV2(envelope)
	if err != nil {
		t.Fatalf("CanonicalJSONV2() error = %v", err)
	}
	return payload
}

func readerLimits() contract.ReaderLimitsV2 {
	return contract.ReaderLimitsV2{
		MaxEnvelopeBytes: 1 << 20, MaxRecordsPerMessage: 100, MaxPlansPerMessage: 10, MaxLevelsPerPlan: 10,
		MaxSelectorBytes: 1 << 16, MaxRecordBytes: 1 << 16, MaxPlanSetBytes: 1 << 18,
		MaxContractDepth: 32, MaxStringBytes: 1 << 16, MaxValidationIssues: 100,
	}
}
