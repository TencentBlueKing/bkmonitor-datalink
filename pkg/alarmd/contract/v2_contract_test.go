// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package contract

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestCanonicalJSONV2AndRecordIdentity(t *testing.T) {
	t.Parallel()

	canonical, err := CanonicalJSONV2(json.RawMessage(`{"z":1e2,"a":"<>&你好"}`))
	if err != nil {
		t.Fatalf("CanonicalJSONV2() error = %v", err)
	}
	if got, want := string(canonical), `{"a":"<>&你好","z":1e2}`; got != want {
		t.Fatalf("CanonicalJSONV2() = %s, want %s", got, want)
	}
	canonical, err = CanonicalJSONV2(json.RawMessage("{\"actual\":\"\\u2028\",\"literal\":\"\\\\u2028\"}"))
	if err != nil {
		t.Fatalf("CanonicalJSONV2(line separator) error = %v", err)
	}
	if got, want := string(canonical), "{\"actual\":\"\u2028\",\"literal\":\"\\\\u2028\"}"; got != want {
		t.Fatalf("CanonicalJSONV2(line separator) = %q, want %q", got, want)
	}

	fields := []DimensionFieldV2{
		{Name: "host", Value: json.RawMessage(`"127.0.0.1"`)},
		{Name: "port", Value: json.RawMessage(`8080`)},
	}
	dimensionDigest, err := DeriveDimensionIdentityDigestV2("default", "2", fields)
	if err != nil {
		t.Fatalf("DeriveDimensionIdentityDigestV2() error = %v", err)
	}
	recordID, err := DeriveRecordIDV2(dimensionDigest, 1_725_000_000)
	if err != nil {
		t.Fatalf("DeriveRecordIDV2() error = %v", err)
	}
	if len(dimensionDigest) != 64 || len(recordID) != 64 {
		t.Fatalf("digests must be sha256 hex: dimension=%q record=%q", dimensionDigest, recordID)
	}
	if got, want := dimensionDigest, "3c92064a5bc0c7703ba6c85ab0714fdfc40aea0bfb7e76efdb5c189ac6076f16"; got != want {
		t.Fatalf("dimension digest = %s, want checked-in Go vector %s", got, want)
	}
	if got, want := recordID, "846d0304a0f24f68e1c7f81e75e039362b3adf7c12cffc2909d92a329ef773ba"; got != want {
		t.Fatalf("record id = %s, want checked-in Go vector %s", got, want)
	}
	kafkaKey, err := DeriveQueryGroupKafkaKeyV2("default", "query-group-1")
	if err != nil {
		t.Fatalf("DeriveQueryGroupKafkaKeyV2() error = %v", err)
	}
	if got, want := fmt.Sprintf("%x", kafkaKey), "999313d003139579dfda9109cf8cc803342b25767a6405e1fb48ead692aa1388"; got != want {
		t.Fatalf("Query Group Kafka key = %s, want %s", got, want)
	}
	vectorPayload, err := os.ReadFile("testdata/go-v2/canonical_vectors.json")
	if err != nil {
		t.Fatalf("os.ReadFile(canonical vectors) error = %v", err)
	}
	var vectors struct {
		DimensionIdentity struct {
			Digest   string `json:"digest"`
			RecordID string `json:"record_id"`
		} `json:"dimension_identity"`
		NegativeBusinessIdentity struct {
			BusinessID string             `json:"business_id"`
			Digest     string             `json:"digest"`
			Fields     []DimensionFieldV2 `json:"fields"`
			RecordID   string             `json:"record_id"`
			SourceTime int64              `json:"source_time"`
			TenantID   string             `json:"tenant_id"`
		} `json:"negative_business_identity"`
	}
	if err := json.Unmarshal(vectorPayload, &vectors); err != nil {
		t.Fatalf("json.Unmarshal(canonical vectors) error = %v", err)
	}
	if vectors.DimensionIdentity.Digest != dimensionDigest || vectors.DimensionIdentity.RecordID != recordID {
		t.Fatalf("checked-in vector drift: %#v", vectors.DimensionIdentity)
	}
	negativeDigest, err := DeriveDimensionIdentityDigestV2(
		vectors.NegativeBusinessIdentity.TenantID,
		vectors.NegativeBusinessIdentity.BusinessID,
		vectors.NegativeBusinessIdentity.Fields,
	)
	if err != nil {
		t.Fatalf("DeriveDimensionIdentityDigestV2(negative Golden) error = %v", err)
	}
	negativeRecordID, err := DeriveRecordIDV2(negativeDigest, vectors.NegativeBusinessIdentity.SourceTime)
	if err != nil {
		t.Fatalf("DeriveRecordIDV2(negative Golden) error = %v", err)
	}
	if negativeDigest != vectors.NegativeBusinessIdentity.Digest || negativeRecordID != vectors.NegativeBusinessIdentity.RecordID {
		t.Fatalf("checked-in negative-business vector drift: %#v", vectors.NegativeBusinessIdentity)
	}

	reordered := []DimensionFieldV2{fields[1], fields[0]}
	if _, err := DeriveDimensionIdentityDigestV2("default", "2", reordered); err == nil {
		t.Fatal("DeriveDimensionIdentityDigestV2() accepted unsorted fields")
	}
	retryID, err := DeriveRecordIDV2(dimensionDigest, 1_725_000_000)
	if err != nil || retryID != recordID {
		t.Fatalf("record retry identity = (%q, %v), want (%q, nil)", retryID, err, recordID)
	}
	if _, err := DeriveDimensionIdentityDigestV2("default", "2", []DimensionFieldV2{}); err != nil {
		t.Fatalf("dimensionless time series must retain a stable business-scoped identity: %v", err)
	}
}

func TestV2BusinessIDUsesCanonicalSignedDecimal(t *testing.T) {
	t.Parallel()

	fields := []DimensionFieldV2{{Name: "host", Value: json.RawMessage(`"127.0.0.1"`)}}
	digest, err := DeriveDimensionIdentityDigestV2("default", "-200", fields)
	if err != nil {
		t.Fatalf("DeriveDimensionIdentityDigestV2(negative business) error = %v", err)
	}
	if len(digest) != 64 {
		t.Fatalf("negative business dimension digest = %q, want sha256 hex", digest)
	}

	for _, businessID := range []string{"", "-0", "+1", "01", "-01"} {
		if _, err := DeriveDimensionIdentityDigestV2("default", businessID, fields); err == nil {
			t.Fatalf("DeriveDimensionIdentityDigestV2() accepted non-canonical business_id %q", businessID)
		}
	}

	envelope := validExecutionEnvelopeV2(t)
	envelope.Records[0].BusinessID = "-200"
	envelope.Records[0].DimensionIdentity.Digest, err = DeriveDimensionIdentityDigestV2(
		envelope.TenantID, envelope.Records[0].BusinessID, envelope.Records[0].DimensionIdentity.Fields,
	)
	if err != nil {
		t.Fatalf("DeriveDimensionIdentityDigestV2(envelope) error = %v", err)
	}
	envelope.Records[0].RecordID, err = DeriveRecordIDV2(
		envelope.Records[0].DimensionIdentity.Digest, envelope.Records[0].SourceTime,
	)
	if err != nil {
		t.Fatalf("DeriveRecordIDV2(envelope) error = %v", err)
	}
	if _, issues, err := ReadExecutionEnvelopeV2(
		encodeExecutionEnvelopeV2ForTest(t, envelope), generousReaderLimitsV2(),
	); err != nil || len(issues) != 0 {
		t.Fatalf("ReadExecutionEnvelopeV2(negative business) = (_, %#v, %v), want success", issues, err)
	}

	event, err := BuildTriggerEventV1(TriggerEventBuildInputV1{
		EventKind: TriggerEventAbnormal, TenantID: "default", BusinessID: "-200",
		PlanRef: RuntimePlanRefV1{
			StrategyID: "1001", StrategyRevision: "strategy-r1", StateCompatibilityHash: strings.Repeat("a", 64),
		},
		RecordRef: TriggerRecordRefV1{
			RecordID: strings.Repeat("b", 64), SourceTime: 1_725_000_000,
			DimensionIdentityDigest: strings.Repeat("c", 64), Dimensions: map[string]json.RawMessage{},
		},
		Observed: TriggerObservedV1{Values: map[string]json.RawMessage{"value": json.RawMessage(`50.1`)}, Unit: "percent"},
		LevelResults: []LevelResultV1{
			levelResultV1ForTest(5, 1, LevelResultAbnormal, strings.Repeat("e", 64)),
		},
		EvaluationTime: 1_725_000_060, DetectPlanFingerprint: strings.Repeat("f", 64),
		TriggerStateFingerprint: strings.Repeat("0", 64), ExecutionID: "execution-negative-business",
		MaxEvidenceBytes: 4096,
	})
	if err != nil {
		t.Fatalf("BuildTriggerEventV1(negative business) error = %v", err)
	}
	payload, err := EncodeTriggerEventV1(event)
	if err != nil {
		t.Fatalf("EncodeTriggerEventV1(negative business) error = %v", err)
	}
	if _, err := DecodeTriggerEventV1(payload); err != nil {
		t.Fatalf("DecodeTriggerEventV1(negative business) error = %v", err)
	}
}

func TestReadExecutionEnvelopeV2PreservesHigherMinorOptionalFields(t *testing.T) {
	t.Parallel()

	payload := encodeExecutionEnvelopeV2ForTest(t, validExecutionEnvelopeV2(t))
	var object map[string]json.RawMessage
	if err := json.Unmarshal(payload, &object); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	object["schema"] = json.RawMessage(`{"name":"execution-envelope","major":2,"minor":1}`)
	object["future_optional"] = json.RawMessage(`{"preserved":true}`)
	payload = recomputeEnvelopeDigestsForRawTest(t, object)

	framed, issues, err := ReadExecutionEnvelopeV2(payload, generousReaderLimitsV2())
	if err != nil || len(issues) != 0 {
		t.Fatalf("ReadExecutionEnvelopeV2() = (_, %#v, %v), want no issues", issues, err)
	}
	if !bytes.Contains(framed.RawPayload, []byte(`"future_optional":{"preserved":true}`)) {
		t.Fatalf("RawPayload did not preserve higher-minor field: %s", framed.RawPayload)
	}
}

func TestExecutionEnvelopeV2EmptySelectorIsExplicit(t *testing.T) {
	t.Parallel()

	envelope := validExecutionEnvelopeV2(t)
	empty := []SelectorRangeV2{}
	envelope.Selectors[0].Selector.Ranges = &empty
	payload := encodeExecutionEnvelopeV2ForTest(t, envelope)
	if !bytes.Contains(payload, []byte(`"ranges":[]`)) {
		t.Fatalf("encoded empty selector is not explicit: %s", payload)
	}
	if _, issues, err := ReadExecutionEnvelopeV2(payload, generousReaderLimitsV2()); err != nil || len(issues) != 0 {
		t.Fatalf("ReadExecutionEnvelopeV2(empty selector) = (_, %#v, %v), want success", issues, err)
	}
}

func TestExecutionEnvelopeV2BitmapSelectorIsLSBFirst(t *testing.T) {
	t.Parallel()

	envelope := validExecutionEnvelopeV2(t)
	envelope.Selectors[0].Selector = SelectorV2{
		Kind:      SelectorKindBitmap,
		BitmapB64: "AQ==",
	}
	payload := encodeExecutionEnvelopeV2ForTest(t, envelope)
	if _, issues, err := ReadExecutionEnvelopeV2(payload, generousReaderLimitsV2()); err != nil || len(issues) != 0 {
		t.Fatalf("ReadExecutionEnvelopeV2(LSB-first bitmap) = (_, %#v, %v), want success", issues, err)
	}

	envelope.Selectors[0].Selector.BitmapB64 = "gA=="
	payload = encodeExecutionEnvelopeV2ForTest(t, envelope)
	if _, issues, err := ReadExecutionEnvelopeV2(payload, generousReaderLimitsV2()); err != nil || len(issues) != 1 || issues[0].ReasonCode != ReasonSelectorInvalid {
		t.Fatalf("ReadExecutionEnvelopeV2(unused high bit) = (_, %#v, %v), want one SELECTOR_INVALID issue", issues, err)
	}
}

func TestSelectorIndexViewV2UsesRangesAndLSBFirstBitmap(t *testing.T) {
	t.Parallel()

	ranges := []SelectorRangeV2{{Start: 1, End: 3}, {Start: 5, End: 6}}
	tests := []struct {
		name       string
		selector   SelectorV2
		records    int
		want       []uint32
		wantLength int
	}{
		{
			name: "ranges", selector: SelectorV2{Kind: SelectorKindRanges, Ranges: &ranges}, records: 10,
			want: []uint32{1, 2, 5}, wantLength: 3,
		},
		{
			name: "bitmap across bytes", selector: SelectorV2{Kind: SelectorKindBitmap, BitmapB64: "gQM="}, records: 10,
			want: []uint32{0, 7, 8, 9}, wantLength: 4,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			view, err := NewSelectorIndexViewV2(test.selector, test.records)
			if err != nil {
				t.Fatalf("NewSelectorIndexViewV2() error = %v", err)
			}
			indexes := make([]uint32, 0, view.Len())
			view.ForEach(func(index uint32) bool {
				indexes = append(indexes, index)
				return true
			})
			if view.Len() != test.wantLength || !equalUint32s(indexes, test.want) {
				t.Fatalf("view = len %d indexes %v, want len %d indexes %v", view.Len(), indexes, test.wantLength, test.want)
			}
		})
	}
}

func TestReadExecutionEnvelopeV2DynamicLevel(t *testing.T) {
	t.Parallel()

	envelope := validExecutionEnvelopeV2(t)
	payload := encodeExecutionEnvelopeV2ForTest(t, envelope)
	framed, issues, err := ReadExecutionEnvelopeV2(payload, generousReaderLimitsV2())
	if err != nil {
		t.Fatalf("ReadExecutionEnvelopeV2() error = %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("ReadExecutionEnvelopeV2() issues = %#v", issues)
	}
	levels := framed.Envelope.PlanSet.EvaluationPlans[0].StrategyIR.Levels
	if got, want := []uint32{levels[0].Definition.LevelID, levels[1].Definition.LevelID}, []uint32{1, 5}; !equalUint32s(got, want) {
		t.Fatalf("level ids = %v, want %v", got, want)
	}
	if levels[0].Definition.Priority != 20 || levels[1].Definition.Priority != 1 {
		t.Fatalf("priority must be independent from level id: %#v", levels)
	}
}

func TestReadExecutionEnvelopeV2QueryCompleteness(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		result      QueryResultV2
		empty       bool
		wantFraming bool
	}{
		{name: "full empty", result: QueryResultV2{Completeness: QueryCompletenessFull}, empty: true},
		{name: "partial", result: QueryResultV2{Completeness: QueryCompletenessPartial, ReasonCode: ReasonQueryPartial}},
		{name: "unavailable", result: QueryResultV2{Completeness: QueryCompletenessUnavailable, ReasonCode: ReasonQueryUnavailable}, empty: true},
		{name: "partial missing reason", result: QueryResultV2{Completeness: QueryCompletenessPartial}, wantFraming: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			envelope := validExecutionEnvelopeV2(t)
			envelope.QueryResult = test.result
			if test.empty {
				empty := []SelectorRangeV2{}
				envelope.Selectors[0].Selector.Ranges = &empty
				envelope.Records = []CanonicalRecordV2{}
			}
			_, issues, err := ReadExecutionEnvelopeV2(encodeExecutionEnvelopeV2ForTest(t, envelope), generousReaderLimitsV2())
			if test.wantFraming {
				if err == nil {
					t.Fatalf("ReadExecutionEnvelopeV2() = (_, %#v, nil), want framing error", issues)
				}
				return
			}
			if err != nil || len(issues) != 0 {
				t.Fatalf("ReadExecutionEnvelopeV2() = (_, %#v, %v), want success", issues, err)
			}
		})
	}
}

func TestExecutionEnvelopeV2GoldenFile(t *testing.T) {
	t.Parallel()

	payload, err := os.ReadFile("testdata/go-v2/execution_envelope_v2.json")
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}
	canonical, err := CanonicalJSONV2(payload)
	if err != nil {
		t.Fatalf("CanonicalJSONV2() error = %v", err)
	}
	if !bytes.Equal(bytes.TrimSpace(payload), canonical) {
		t.Fatal("golden ExecutionEnvelope is not canonical JSON")
	}
	framed, issues, err := ReadExecutionEnvelopeV2(canonical, generousReaderLimitsV2())
	if err != nil || len(issues) != 0 {
		t.Fatalf("ReadExecutionEnvelopeV2(golden) = (_, %#v, %v)", issues, err)
	}
	if bytes.Contains(framed.RawPayload, []byte(`"expression"`)) ||
		bytes.Contains(framed.RawPayload, []byte(`"state_compatibility_hash"`)) ||
		bytes.Contains(framed.RawPayload, []byte(`"execution_limits"`)) {
		t.Fatal("producer wire must not contain Access expression, compiled state identity, or execution limits")
	}
}

func TestExecutionEnvelopeV2TerminalPlanGoldenFile(t *testing.T) {
	t.Parallel()

	envelope := validExecutionEnvelopeV2(t)
	executable := envelope.PlanSet.EvaluationPlans[0]
	envelope.PlanSet.EvaluationPlans[0] = EvaluationPlanV2{
		PlanID: executable.PlanID, StrategyRef: executable.StrategyRef,
		TerminalReasonCode: ReasonMultipleEvaluationUnitsUnsupported,
	}
	payload := encodeExecutionEnvelopeV2ForTest(t, envelope)
	assertGoldenPayloadV2(t, "testdata/go-v2/execution_envelope_terminal_plan_v2.json", payload)

	var wire struct {
		PlanSet struct {
			Plans []map[string]json.RawMessage `json:"evaluation_plans"`
		} `json:"plan_set"`
	}
	if err := json.Unmarshal(payload, &wire); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if got := wire.PlanSet.Plans[0]; len(got) != 3 || got["plan_id"] == nil || got["strategy_ref"] == nil || got["terminal_reason_code"] == nil {
		t.Fatalf("terminal Plan shape = %#v, want exactly plan_id + strategy_ref + terminal_reason_code", got)
	}

	framed, issues, err := ReadExecutionEnvelopeV2(payload, generousReaderLimitsV2())
	if err != nil {
		t.Fatalf("ReadExecutionEnvelopeV2() error = %v", err)
	}
	if framed == nil || len(issues) != 1 || issues[0].Scope != ValidationScopePlan ||
		issues[0].ReasonCode != ReasonMultipleEvaluationUnitsUnsupported || issues[0].PlanOrdinal == nil || *issues[0].PlanOrdinal != 0 {
		t.Fatalf("ReadExecutionEnvelopeV2() issues = %#v, want one Plan terminal", issues)
	}
}

func TestReadExecutionEnvelopeV2TerminalAndExecutablePlanAreMutuallyExclusive(t *testing.T) {
	t.Parallel()

	payload := encodeExecutionEnvelopeV2ForTest(t, validExecutionEnvelopeV2(t))
	var object map[string]json.RawMessage
	if err := json.Unmarshal(payload, &object); err != nil {
		t.Fatal(err)
	}
	var planSet map[string]json.RawMessage
	if err := json.Unmarshal(object["plan_set"], &planSet); err != nil {
		t.Fatal(err)
	}
	var plans []map[string]json.RawMessage
	if err := json.Unmarshal(planSet["evaluation_plans"], &plans); err != nil {
		t.Fatal(err)
	}
	plans[0]["terminal_reason_code"] = json.RawMessage(`"MULTIPLE_EVALUATION_UNITS_UNSUPPORTED"`)
	planSet["evaluation_plans"], _ = CanonicalJSONV2(plans)
	object["plan_set"], _ = CanonicalJSONV2(planSet)
	payload = recomputeEnvelopeDigestsForRawTest(t, object)

	_, issues, err := ReadExecutionEnvelopeV2(payload, generousReaderLimitsV2())
	if err != nil {
		t.Fatalf("ReadExecutionEnvelopeV2() error = %v", err)
	}
	if len(issues) != 1 || issues[0].Scope != ValidationScopePlan || issues[0].ReasonCode != ReasonPlanInvalid ||
		issues[0].PlanIdentityUntrusted {
		t.Fatalf("issues = %#v, want mixed Plan variant isolated with trusted Plan identity", issues)
	}
}

func TestGoV2GoldenChecksums(t *testing.T) {
	t.Parallel()

	manifest, err := os.ReadFile("testdata/go-v2/SHA256SUMS")
	if err != nil {
		t.Fatalf("os.ReadFile(SHA256SUMS) error = %v", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(manifest)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			t.Fatalf("invalid checksum line %q", line)
		}
		payload, err := os.ReadFile("testdata/go-v2/" + fields[1])
		if err != nil {
			t.Fatalf("os.ReadFile(%s) error = %v", fields[1], err)
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(payload)); got != fields[0] {
			t.Fatalf("checksum(%s) = %s, want %s", fields[1], got, fields[0])
		}
	}
}

func TestGoV2InvalidVectors(t *testing.T) {
	t.Parallel()

	payload, err := os.ReadFile("testdata/go-v2/invalid_vectors.json")
	if err != nil {
		t.Fatalf("os.ReadFile(invalid vectors) error = %v", err)
	}
	var vectors struct {
		BusinessIDs []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
			Valid bool   `json:"valid"`
		} `json:"business_ids"`
		Selectors []struct {
			Name        string   `json:"name"`
			BitmapB64   string   `json:"bitmap_b64"`
			RecordCount int      `json:"record_count"`
			Selected    []uint32 `json:"selected"`
			Valid       bool     `json:"valid"`
		} `json:"selectors"`
		LevelResults []struct {
			Name              string `json:"name"`
			WindowSize        uint32 `json:"window_size"`
			RequiredAnomalies uint32 `json:"required_anomalies"`
			ObservedAnomalies uint32 `json:"observed_anomalies"`
			Result            string `json:"result"`
			Valid             bool   `json:"valid"`
		} `json:"level_results"`
	}
	if err := json.Unmarshal(payload, &vectors); err != nil {
		t.Fatalf("json.Unmarshal(invalid vectors) error = %v", err)
	}
	for _, vector := range vectors.BusinessIDs {
		if got := canonicalSignedDecimalPattern.MatchString(vector.Value); got != vector.Valid {
			t.Fatalf("business id vector %q validity = %v, want %v", vector.Name, got, vector.Valid)
		}
	}
	for _, vector := range vectors.Selectors {
		view, err := NewSelectorIndexViewV2(SelectorV2{Kind: SelectorKindBitmap, BitmapB64: vector.BitmapB64}, vector.RecordCount)
		if (err == nil) != vector.Valid {
			t.Fatalf("selector %s error = %v, valid=%t", vector.Name, err, vector.Valid)
		}
		if err == nil {
			selected := make([]uint32, 0, view.Len())
			view.ForEach(func(index uint32) bool { selected = append(selected, index); return true })
			if !equalUint32s(selected, vector.Selected) {
				t.Fatalf("selector %s indexes = %v, want %v", vector.Name, selected, vector.Selected)
			}
		}
	}
	for _, vector := range vectors.LevelResults {
		result := levelResultV1ForTest(5, 1, vector.Result, strings.Repeat("e", 64))
		result.DecisionWindow.Trigger.WindowSize = vector.WindowSize
		result.DecisionWindow.Trigger.RequiredAnomalies = vector.RequiredAnomalies
		result.DecisionWindow.Trigger.ObservedAnomalies = vector.ObservedAnomalies
		err := validateSuccessfulLevelResultsV1([]LevelResultV1{result})
		if (err == nil) != vector.Valid {
			t.Fatalf("level result %s error = %v, valid=%t", vector.Name, err, vector.Valid)
		}
	}
}

func TestReadExecutionEnvelopeV2StrictFraming(t *testing.T) {
	t.Parallel()

	valid := validExecutionEnvelopeV2(t)
	payload := encodeExecutionEnvelopeV2ForTest(t, valid)

	tests := map[string][]byte{
		"bom":                  append([]byte{0xef, 0xbb, 0xbf}, payload...),
		"trailing value":       append(append([]byte(nil), payload...), []byte(` {}`)...),
		"duplicate field":      bytes.Replace(payload, []byte(`"execution_id":"execution-1"`), []byte(`"execution_id":"execution-1","execution_id":"execution-2"`), 1),
		"nested duplicate":     bytes.Replace(payload, []byte(`"source_time":1725000000`), []byte(`"source_time":1725000000,"source_time":1725000001`), 1),
		"case collision":       append(payload[:len(payload)-1], []byte(`,"Execution_ID":"execution-1"}`)...),
		"payload digest drift": bytes.Replace(payload, []byte(`"query_revision":"query-r1"`), []byte(`"query_revision":"query-r2"`), 1),
	}
	for name, candidate := range tests {
		name, candidate := name, candidate
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, _, err := ReadExecutionEnvelopeV2(candidate, generousReaderLimitsV2()); err == nil {
				t.Fatal("ReadExecutionEnvelopeV2() error = nil, want framing error")
			} else {
				var framing *MessageFramingError
				if !errors.As(err, &framing) {
					t.Fatalf("error type = %T, want *MessageFramingError", err)
				}
			}
		})
	}
}

func TestReadExecutionEnvelopeV2ReturnsBoundedScopedIssues(t *testing.T) {
	t.Parallel()

	envelope := validExecutionEnvelopeV2(t)
	plan := &envelope.PlanSet.EvaluationPlans[0]
	plan.StrategyIR.Levels = append(plan.StrategyIR.Levels, plan.StrategyIR.Levels[1])
	envelope.Records[0].RecordID = strings.Repeat("0", 64)
	payload := encodeExecutionEnvelopeV2ForTest(t, envelope)

	framed, issues, err := ReadExecutionEnvelopeV2(payload, generousReaderLimitsV2())
	if err != nil {
		t.Fatalf("ReadExecutionEnvelopeV2() error = %v", err)
	}
	if framed == nil {
		t.Fatal("ReadExecutionEnvelopeV2() framed = nil")
	}
	if !containsIssue(issues, ValidationScopePlan, ReasonPlanDuplicateLevelID) {
		t.Fatalf("issues = %#v, want PLAN_DUPLICATE_LEVEL_ID", issues)
	}
	if !containsIssue(issues, ValidationScopeRecord, ReasonRecordIdentityConflict) {
		t.Fatalf("issues = %#v, want RECORD_IDENTITY_CONFLICT", issues)
	}

	limits := generousReaderLimitsV2()
	limits.MaxValidationIssues = 1
	_, limited, err := ReadExecutionEnvelopeV2(payload, limits)
	if err != nil {
		t.Fatalf("ReadExecutionEnvelopeV2(limited) error = %v", err)
	}
	if len(limited) != 1 || limited[0].ReasonCode != ReasonValidationBudgetExceeded {
		t.Fatalf("limited issues = %#v, want one VALIDATION_BUDGET_EXCEEDED", limited)
	}
	if limited[0].UnverifiedTail == nil || pointerValue(limited[0].UnverifiedTail.PlanFromOrdinal) != 0 ||
		pointerValue(limited[0].UnverifiedTail.RecordFromOrdinal) != 0 {
		t.Fatalf("limited tail = %#v, want plans[0:] and records[0:] explicitly unverified", limited[0].UnverifiedTail)
	}
}

func TestReadExecutionEnvelopeV2DistinguishesUntrustedPlanIdentity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		mutate        func(*EvaluationPlanV2)
		wantUntrusted bool
	}{
		{
			name: "valid identity with invalid body",
			mutate: func(plan *EvaluationPlanV2) {
				plan.StrategyIR.Levels = nil
			},
		},
		{
			name: "invalid identity",
			mutate: func(plan *EvaluationPlanV2) {
				plan.PlanID = "invalid"
			},
			wantUntrusted: true,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			envelope := validExecutionEnvelopeV2(t)
			test.mutate(&envelope.PlanSet.EvaluationPlans[0])
			_, issues, err := ReadExecutionEnvelopeV2(encodeExecutionEnvelopeV2ForTest(t, envelope), generousReaderLimitsV2())
			if err != nil || len(issues) != 1 || issues[0].ReasonCode != ReasonPlanInvalid {
				t.Fatalf("ReadExecutionEnvelopeV2() = (_, %#v, %v), want one PLAN_INVALID", issues, err)
			}
			if issues[0].PlanIdentityUntrusted != test.wantUntrusted {
				t.Fatalf("PlanIdentityUntrusted = %t, want %t", issues[0].PlanIdentityUntrusted, test.wantUntrusted)
			}
		})
	}
}

func TestReadExecutionEnvelopeV2IsolatesMalformedLevelWireShape(t *testing.T) {
	t.Parallel()

	payload := encodeExecutionEnvelopeV2ForTest(t, validExecutionEnvelopeV2(t))
	payload = bytes.Replace(payload, []byte(`"level_id":1,"priority":20`), []byte(`"level_id":1,"priority":"invalid"`), 1)
	var object map[string]json.RawMessage
	if err := json.Unmarshal(payload, &object); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	payload = recomputeEnvelopeDigestsForRawTest(t, object)

	_, issues, err := ReadExecutionEnvelopeV2(payload, generousReaderLimitsV2())
	if err != nil {
		t.Fatalf("ReadExecutionEnvelopeV2() error = %v", err)
	}
	if len(issues) != 1 || issues[0].Scope != ValidationScopeLevel || issues[0].ReasonCode != ReasonLevelInvalid ||
		issues[0].LevelID == nil || *issues[0].LevelID != 1 {
		t.Fatalf("issues = %#v, want only level 1 LEVEL_INVALID", issues)
	}
}

func TestReadExecutionEnvelopeV2IsolatesMalformedPlanAndRecordTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		old        string
		new        string
		wantScope  ValidationScope
		wantReason string
	}{
		{name: "plan", old: `"plan_id":"1001"`, new: `"plan_id":1001`, wantScope: ValidationScopePlan, wantReason: ReasonPlanInvalid},
		{name: "record", old: `"received_time":1725000001`, new: `"received_time":"invalid"`, wantScope: ValidationScopeRecord, wantReason: ReasonRecordInvalid},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			payload := encodeExecutionEnvelopeV2ForTest(t, validExecutionEnvelopeV2(t))
			payload = bytes.Replace(payload, []byte(test.old), []byte(test.new), 1)
			var object map[string]json.RawMessage
			if err := json.Unmarshal(payload, &object); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}
			payload = recomputeEnvelopeDigestsForRawTest(t, object)
			_, issues, err := ReadExecutionEnvelopeV2(payload, generousReaderLimitsV2())
			if err != nil {
				t.Fatalf("ReadExecutionEnvelopeV2() error = %v", err)
			}
			if !containsIssue(issues, test.wantScope, test.wantReason) {
				t.Fatalf("issues = %#v, want %s/%s", issues, test.wantScope, test.wantReason)
			}
		})
	}
}

func TestReadExecutionEnvelopeV2ValidatesRecordDatasetContract(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		mutate     func(*ExecutionEnvelopeV2)
		wantReason string
	}{
		{
			name: "identity field set",
			mutate: func(envelope *ExecutionEnvelopeV2) {
				envelope.DatasetContract.IdentityFields = []string{"other"}
			},
			wantReason: ReasonRecordIdentityConflict,
		},
		{
			name: "identity dimension value",
			mutate: func(envelope *ExecutionEnvelopeV2) {
				envelope.Records[0].Dimensions["host"] = json.RawMessage(`"other"`)
			},
			wantReason: ReasonRecordIdentityConflict,
		},
		{
			name: "nested value",
			mutate: func(envelope *ExecutionEnvelopeV2) {
				envelope.Records[0].Values["value"] = json.RawMessage(`{"nested":1}`)
			},
			wantReason: ReasonRecordInvalid,
		},
		{
			name: "nested dimension",
			mutate: func(envelope *ExecutionEnvelopeV2) {
				envelope.Records[0].Dimensions["extra"] = json.RawMessage(`[1]`)
			},
			wantReason: ReasonRecordInvalid,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			envelope := validExecutionEnvelopeV2(t)
			test.mutate(envelope)
			_, issues, err := ReadExecutionEnvelopeV2(encodeExecutionEnvelopeV2ForTest(t, envelope), generousReaderLimitsV2())
			if err != nil {
				t.Fatalf("ReadExecutionEnvelopeV2() error = %v", err)
			}
			if !containsIssue(issues, ValidationScopeRecord, test.wantReason) {
				t.Fatalf("issues = %#v, want RECORD/%s", issues, test.wantReason)
			}
		})
	}
}

func TestReadExecutionEnvelopeV2AllowsDimensionlessSeriesAndIsolatesCrossBusinessRecord(t *testing.T) {
	t.Parallel()

	dimensionless := validExecutionEnvelopeV2(t)
	dimensionless.DatasetContract.IdentityFields = []string{}
	dimensionless.Records[0].DimensionIdentity.Fields = []DimensionFieldV2{}
	dimensionless.Records[0].Dimensions = map[string]json.RawMessage{}
	digest, err := DeriveDimensionIdentityDigestV2(dimensionless.TenantID, dimensionless.Records[0].BusinessID, []DimensionFieldV2{})
	if err != nil {
		t.Fatalf("DeriveDimensionIdentityDigestV2() error = %v", err)
	}
	dimensionless.Records[0].DimensionIdentity.Digest = digest
	dimensionless.Records[0].RecordID, err = DeriveRecordIDV2(digest, dimensionless.Records[0].SourceTime)
	if err != nil {
		t.Fatalf("DeriveRecordIDV2() error = %v", err)
	}
	if _, issues, err := ReadExecutionEnvelopeV2(encodeExecutionEnvelopeV2ForTest(t, dimensionless), generousReaderLimitsV2()); err != nil || len(issues) != 0 {
		t.Fatalf("ReadExecutionEnvelopeV2(dimensionless) = (_, %#v, %v), want success", issues, err)
	}

	envelope := validExecutionEnvelopeV2(t)
	base := envelope.Records[0]
	records := make([]CanonicalRecordV2, 3)
	for index := range records {
		records[index] = base
		records[index].SourceTime += int64(index * 60)
		records[index].ReceivedTime += int64(index * 60)
		if index == 1 {
			records[index].BusinessID = "3"
		}
		dimensionDigest, deriveErr := DeriveDimensionIdentityDigestV2(envelope.TenantID, records[index].BusinessID, records[index].DimensionIdentity.Fields)
		if deriveErr != nil {
			t.Fatalf("DeriveDimensionIdentityDigestV2(record %d) error = %v", index, deriveErr)
		}
		records[index].DimensionIdentity.Digest = dimensionDigest
		records[index].RecordID, deriveErr = DeriveRecordIDV2(dimensionDigest, records[index].SourceTime)
		if deriveErr != nil {
			t.Fatalf("DeriveRecordIDV2(record %d) error = %v", index, deriveErr)
		}
	}
	envelope.Records = records
	envelope.SourceWindow.UntilTime = records[len(records)-1].SourceTime + 60
	ranges := []SelectorRangeV2{{Start: 0, End: 3}}
	envelope.Selectors[0].Selector.Ranges = &ranges
	_, issues, err := ReadExecutionEnvelopeV2(encodeExecutionEnvelopeV2ForTest(t, envelope), generousReaderLimitsV2())
	if err != nil {
		t.Fatalf("ReadExecutionEnvelopeV2(cross business) error = %v", err)
	}
	if !containsRecordIssueV2(issues, 1, ReasonRecordIdentityConflict) || containsRecordIssueV2(issues, 2, ReasonRecordIdentityConflict) {
		t.Fatalf("issues = %#v, want only cross-business record ordinal 1 isolated", issues)
	}
}

func TestReadExecutionEnvelopeV2IsolatesRecordOutsideSourceWindow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		sourceTime func(SourceWindowV2) int64
		wantIssue  bool
	}{
		{name: "at from", sourceTime: func(window SourceWindowV2) int64 { return window.FromTime }},
		{name: "before from", sourceTime: func(window SourceWindowV2) int64 { return window.FromTime - 1 }, wantIssue: true},
		{name: "before until", sourceTime: func(window SourceWindowV2) int64 { return window.UntilTime - 1 }},
		{name: "at until", sourceTime: func(window SourceWindowV2) int64 { return window.UntilTime }, wantIssue: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			envelope := validExecutionEnvelopeV2(t)
			record := &envelope.Records[0]
			record.SourceTime = test.sourceTime(envelope.SourceWindow)
			var err error
			record.RecordID, err = DeriveRecordIDV2(record.DimensionIdentity.Digest, record.SourceTime)
			if err != nil {
				t.Fatalf("DeriveRecordIDV2() error = %v", err)
			}
			_, issues, err := ReadExecutionEnvelopeV2(
				encodeExecutionEnvelopeV2ForTest(t, envelope), generousReaderLimitsV2(),
			)
			if err != nil {
				t.Fatalf("ReadExecutionEnvelopeV2() error = %v", err)
			}
			gotIssue := containsRecordIssueV2(issues, 0, ReasonTimeInvalid)
			if gotIssue != test.wantIssue {
				t.Fatalf("TIME_INVALID issue = %t, want %t; issues = %#v", gotIssue, test.wantIssue, issues)
			}
		})
	}
}

func TestReadExecutionEnvelopeV2TerminalizesEveryDuplicateRecordSlot(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		mutate     func(*CanonicalRecordV2)
		wantReason string
	}{
		{name: "same body", mutate: func(*CanonicalRecordV2) {}, wantReason: ReasonRecordInvalid},
		{
			name: "different body",
			mutate: func(record *CanonicalRecordV2) {
				record.ReceivedTime++
			},
			wantReason: ReasonRecordIdentityConflict,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			envelope := validExecutionEnvelopeV2(t)
			duplicate := envelope.Records[0]
			test.mutate(&duplicate)
			envelope.Records = append(envelope.Records, duplicate)
			ranges := []SelectorRangeV2{{Start: 0, End: 2}}
			envelope.Selectors[0].Selector.Ranges = &ranges
			_, issues, err := ReadExecutionEnvelopeV2(
				encodeExecutionEnvelopeV2ForTest(t, envelope), generousReaderLimitsV2(),
			)
			if err != nil {
				t.Fatalf("ReadExecutionEnvelopeV2(duplicate) error = %v", err)
			}
			for ordinal := uint32(0); ordinal < 2; ordinal++ {
				if !containsRecordIssueV2(issues, ordinal, test.wantReason) {
					t.Fatalf("issues = %#v, want record %d %s", issues, ordinal, test.wantReason)
				}
			}
		})
	}
}

func TestReadExecutionEnvelopeV2InvalidSortIdentityDoesNotPolluteSiblings(t *testing.T) {
	t.Parallel()

	t.Run("plan", func(t *testing.T) {
		t.Parallel()
		envelope := validExecutionEnvelopeV2(t)
		invalid := envelope.PlanSet.EvaluationPlans[0]
		invalid.PlanID = "invalid"
		envelope.PlanSet.EvaluationPlans = []EvaluationPlanV2{invalid, envelope.PlanSet.EvaluationPlans[0]}
		envelope.PlanSet.PlanCount = 2
		envelope.Selectors = []PlanSelectorV2{
			{PlanOrdinal: 0, Selector: envelope.Selectors[0].Selector},
			{PlanOrdinal: 1, Selector: envelope.Selectors[0].Selector},
		}
		_, issues, err := ReadExecutionEnvelopeV2(encodeExecutionEnvelopeV2ForTest(t, envelope), generousReaderLimitsV2())
		if err != nil {
			t.Fatalf("ReadExecutionEnvelopeV2() error = %v", err)
		}
		if !hasOnlyPlanOrdinalsV2(issues, 0) {
			t.Fatalf("issues = %#v, invalid Plan identity polluted its valid sibling", issues)
		}
	})

	t.Run("level", func(t *testing.T) {
		t.Parallel()
		envelope := validExecutionEnvelopeV2(t)
		invalid := envelope.PlanSet.EvaluationPlans[0].StrategyIR.Levels[0]
		invalid.Definition.LevelID = 10
		invalid.Definition.Priority = 0
		valid := envelope.PlanSet.EvaluationPlans[0].StrategyIR.Levels[1]
		envelope.PlanSet.EvaluationPlans[0].StrategyIR.Levels = []LevelIRV2{invalid, valid}
		_, issues, err := ReadExecutionEnvelopeV2(encodeExecutionEnvelopeV2ForTest(t, envelope), generousReaderLimitsV2())
		if err != nil {
			t.Fatalf("ReadExecutionEnvelopeV2() error = %v", err)
		}
		if !containsIssue(issues, ValidationScopeLevel, ReasonLevelInvalid) || containsLevelIssueV2(issues, 5) {
			t.Fatalf("issues = %#v, invalid Level identity polluted Level 5", issues)
		}
	})
}

func TestReadExecutionEnvelopeV2ValidationBudgetPreservesBoundedContext(t *testing.T) {
	t.Parallel()

	envelope := validExecutionEnvelopeV2(t)
	record := envelope.Records[0]
	envelope.Records = []CanonicalRecordV2{record, record, record}
	for index := range envelope.Records {
		envelope.Records[index].RecordID = strings.Repeat(string(rune('a'+index)), 64)
	}
	ranges := []SelectorRangeV2{{Start: 0, End: 3}}
	envelope.Selectors[0].Selector.Ranges = &ranges
	limits := generousReaderLimitsV2()
	limits.MaxValidationIssues = 2
	_, issues, err := ReadExecutionEnvelopeV2(encodeExecutionEnvelopeV2ForTest(t, envelope), limits)
	if err != nil {
		t.Fatalf("ReadExecutionEnvelopeV2() error = %v", err)
	}
	if len(issues) != 2 || !containsIssue(issues, ValidationScopeRecord, ReasonRecordIdentityConflict) ||
		!containsIssue(issues, ValidationScopeRecord, ReasonValidationBudgetExceeded) {
		t.Fatalf("issues = %#v, want one located record issue plus one located budget issue", issues)
	}
	budget := issues[1]
	if budget.ReasonCode != ReasonValidationBudgetExceeded || budget.UnverifiedTail == nil ||
		budget.UnverifiedTail.PlanFromOrdinal != nil || pointerValue(budget.UnverifiedTail.RecordFromOrdinal) != 1 {
		t.Fatalf("budget issue = %#v, want records[1:] explicitly unverified", budget)
	}
}

func TestReadExecutionEnvelopeV2BudgetBoundaries(t *testing.T) {
	t.Parallel()

	payload := encodeExecutionEnvelopeV2ForTest(t, validExecutionEnvelopeV2(t))
	limits := generousReaderLimitsV2()
	limits.MaxEnvelopeBytes = len(payload)
	if _, _, err := ReadExecutionEnvelopeV2(payload, limits); err != nil {
		t.Fatalf("exact budget rejected: %v", err)
	}
	limits.MaxEnvelopeBytes--
	if _, _, err := ReadExecutionEnvelopeV2(payload, limits); err == nil {
		t.Fatal("over budget payload accepted")
	}
}

func TestStateCompatibilityAndLevelFingerprintsAreSeparated(t *testing.T) {
	t.Parallel()

	state := StateCompatibilityInputV1{
		StateSchemaVersion:          "window-state-v1",
		CodecSemanticsVersion:       "none-v1",
		IdentitySchemaDigest:        strings.Repeat("1", 64),
		EvaluationScope:             EvaluationScopeSeries,
		AggregationInterval:         60,
		EvaluationInterval:          60,
		SourceTimeSemanticsVersion:  "source-time-v1",
		HistoryCellSemanticsVersion: "valid-anomalous-bitmap-v1",
	}
	first, err := DeriveStateCompatibilityHashV1(state)
	if err != nil {
		t.Fatalf("DeriveStateCompatibilityHashV1() error = %v", err)
	}
	second, err := DeriveStateCompatibilityHashV1(state)
	if err != nil || second != first {
		t.Fatalf("state hash = (%q, %v), want (%q, nil)", second, err, first)
	}

	levelA, err := DeriveLevelDetectFingerprintV1(LevelDetectSemanticV1{
		LevelID: 5, ProjectionDigest: strings.Repeat("2", 64), DetectorSemanticDigest: strings.Repeat("3", 64),
	})
	if err != nil {
		t.Fatalf("DeriveLevelDetectFingerprintV1() error = %v", err)
	}
	levelB, err := DeriveLevelDetectFingerprintV1(LevelDetectSemanticV1{
		LevelID: 5, ProjectionDigest: strings.Repeat("2", 64), DetectorSemanticDigest: strings.Repeat("4", 64),
	})
	if err != nil || levelA == levelB {
		t.Fatalf("level fingerprints = (%q, %q, %v), want distinct", levelA, levelB, err)
	}
	unchanged, err := DeriveStateCompatibilityHashV1(state)
	if err != nil || unchanged != first {
		t.Fatalf("level semantic change altered whole-key hash: got (%q, %v), want %q", unchanged, err, first)
	}
	if got, want := first, "b9b1ac8205afdfc120947f9ea09e031bd4b1425eab9262ce52fb6dcf0055b138"; got != want {
		t.Fatalf("state compatibility hash = %s, want %s", got, want)
	}
	if got, want := levelA, "a6b1d418dd3d1453737456787481787e3d6cbbd3be55a02d70622a409b26b43f"; got != want {
		t.Fatalf("Level detect fingerprint = %s, want %s", got, want)
	}
}

func TestReasonCatalogV2IsFrozenAndDomainAware(t *testing.T) {
	t.Parallel()

	catalog := ReasonCatalogV2()
	if len(catalog) == 0 || !IsKnownReasonV2(ReasonRecordInvalid) || IsKnownReasonV2("UNKNOWN_REASON") {
		t.Fatalf("unexpected Reason catalog: %#v", catalog)
	}
	definition, ok := LookupReasonV2(ReasonRedisUnavailable)
	if !ok || definition.Class != ReasonClassRetryable || !definition.Domains.Has(ReasonDomainObservation) {
		t.Fatalf("Redis reason definition = (%#v, %t)", definition, ok)
	}
	provider, ok := LookupReasonV2(ReasonProviderUnavailable)
	if !ok || provider.Class != ReasonClassRetryable || provider.Domains != ReasonDomainObservation ||
		ReasonAllowedForV2(ReasonProviderUnavailable, ReasonDomainReceipt) {
		t.Fatalf("Provider reason definition = (%#v, %t)", provider, ok)
	}
	if !ReasonAllowedForV2(ReasonQueryPartial, ReasonDomainQueryResult) ||
		ReasonAllowedForV2(ReasonRecordInvalid, ReasonDomainQueryResult) {
		t.Fatal("QueryResult Reason domain accepted an invalid mapping")
	}
	for _, reason := range []string{
		ReasonEffectiveTimeInactive,
		ReasonEffectiveTimeUnknown,
		ReasonHistoryWarming,
		ReasonHistoryGapped,
	} {
		definition, ok := LookupReasonV2(reason)
		if !ok || definition.Class != ReasonClassCoverage ||
			!definition.Domains.Has(ReasonDomainReceipt) || !definition.Domains.Has(ReasonDomainObservation) ||
			definition.Domains.Has(ReasonDomainQueryResult) || definition.Domains.Has(ReasonDomainSummary) {
			t.Fatalf("runtime coverage reason %q definition = (%#v, %t)", reason, definition, ok)
		}
	}
	firstCode := catalog[0].Code
	catalog[0].Code = "MUTATED"
	if refreshed := ReasonCatalogV2(); len(refreshed) == 0 || refreshed[0].Code != firstCode {
		t.Fatal("ReasonCatalogV2 exposed mutable catalog storage")
	}
}

func TestContractsRejectUnknownOrWrongDomainReasonCodes(t *testing.T) {
	t.Parallel()

	for _, reason := range []string{"UNKNOWN_REASON", ReasonRecordInvalid} {
		envelope := validExecutionEnvelopeV2(t)
		envelope.QueryResult = QueryResultV2{Completeness: QueryCompletenessPartial, ReasonCode: reason}
		if _, _, err := ReadExecutionEnvelopeV2(encodeExecutionEnvelopeV2ForTest(t, envelope), generousReaderLimitsV2()); err == nil {
			t.Fatalf("QueryResult accepted reason %q", reason)
		}
	}

	receipt := validMessageReceiptV1ForTest()
	receipt.ReasonCounts = []ReasonCountV1{{ReasonCode: "UNKNOWN_REASON", Count: 1}}
	if _, err := BuildMessageReceiptV1(receipt); err == nil {
		t.Fatal("MessageReceipt accepted an unknown reason")
	}

	summary := validExecutionSummaryV1ForTest()
	summary.Dropped = CountSetV1{Messages: 1, Records: 1, Bytes: 10}
	summary.Published = CountSetV1{}
	summary.ReasonCounts = []ReasonCountV1{{ReasonCode: ReasonRecordInvalid, Count: 1}}
	if _, err := BuildExecutionSummaryV1(summary); err == nil {
		t.Fatal("ExecutionSummary accepted a wrong-domain reason")
	}
}

func TestBuildTriggerEventV1UsesOnlySuccessfulActiveLevelResults(t *testing.T) {
	t.Parallel()

	event, err := BuildTriggerEventV1(TriggerEventBuildInputV1{
		EventKind:  TriggerEventAbnormal,
		TenantID:   "default",
		BusinessID: "2",
		PlanRef: RuntimePlanRefV1{
			StrategyID: "1001", StrategyRevision: "strategy-r1", StateCompatibilityHash: strings.Repeat("a", 64),
		},
		RecordRef: TriggerRecordRefV1{
			RecordID: strings.Repeat("b", 64), SourceTime: 1_725_000_000,
			DimensionIdentityDigest: strings.Repeat("c", 64), Dimensions: map[string]json.RawMessage{"host": json.RawMessage(`"127.0.0.1"`)},
		},
		Observed: TriggerObservedV1{Values: map[string]json.RawMessage{"value": json.RawMessage(`50.1`)}, Unit: "percent"},
		LevelResults: []LevelResultV1{
			levelResultV1ForTest(1, 20, LevelResultNormal, strings.Repeat("d", 64)),
			levelResultV1ForTest(5, 1, LevelResultAbnormal, strings.Repeat("e", 64)),
		},
		EvaluationTime:          1_725_000_060,
		DetectPlanFingerprint:   strings.Repeat("f", 64),
		TriggerStateFingerprint: strings.Repeat("0", 64),
		ExecutionID:             "execution-1",
		MaxEvidenceBytes:        4096,
	})
	if err != nil {
		t.Fatalf("BuildTriggerEventV1() error = %v", err)
	}
	if event.PrimaryLevelID != 5 || len(event.LevelResults) != 2 {
		t.Fatalf("event primary/results = (%d, %#v), want level 5 and two results", event.PrimaryLevelID, event.LevelResults)
	}
	if len(event.EventID) != 64 || len(event.EventSemanticDigest) != 64 {
		t.Fatalf("event ids must be sha256: %#v", event)
	}
	if got, want := event.EventSemanticDigest, "a2d9996e08e1f2a858c88365982da284f887ed78851b1951d267030846271c4e"; got != want {
		t.Fatalf("event semantic digest = %s, want %s", got, want)
	}
	if got, want := event.EventID, "4f11eeeb0400151c3f6d3f26892b1a8ce44c67e79df969933862bccbf3c1ffd3"; got != want {
		t.Fatalf("event id = %s, want %s", got, want)
	}
	eventPayload, err := EncodeTriggerEventV1(event)
	if err != nil {
		t.Fatalf("EncodeTriggerEventV1() error = %v", err)
	}
	assertGoldenPayloadV2(t, "testdata/go-v2/trigger_event_v1.json", eventPayload)
	if _, err := DecodeTriggerEventV1(eventPayload); err != nil {
		t.Fatalf("DecodeTriggerEventV1() error = %v", err)
	}
	unknownEvent := append(append([]byte(nil), eventPayload[:len(eventPayload)-1]...), []byte(`,"future":true}`)...)
	if _, err := DecodeTriggerEventV1(unknownEvent); err == nil {
		t.Fatal("DecodeTriggerEventV1() accepted an unknown 1.0 field")
	}

	var tampered map[string]json.RawMessage
	if err := json.Unmarshal(eventPayload, &tampered); err != nil {
		t.Fatalf("json.Unmarshal(event) error = %v", err)
	}
	var levelResults []map[string]json.RawMessage
	if err := json.Unmarshal(tampered["level_results"], &levelResults); err != nil {
		t.Fatalf("json.Unmarshal(level_results) error = %v", err)
	}
	levelResults[0]["level_trigger_fingerprint"] = json.RawMessage(`"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`)
	tampered["level_results"], err = json.Marshal(levelResults)
	if err != nil {
		t.Fatalf("json.Marshal(level_results) error = %v", err)
	}
	tamperedPayload, err := json.Marshal(tampered)
	if err != nil {
		t.Fatalf("json.Marshal(tampered event) error = %v", err)
	}
	if _, err := DecodeTriggerEventV1(tamperedPayload); err == nil {
		t.Fatal("DecodeTriggerEventV1() accepted tampered Level trigger fingerprint without recomputing event identity")
	}

	bad := TriggerEventBuildInputV1{
		EventKind: TriggerEventAbnormal, TenantID: "default", BusinessID: "2",
		PlanRef: event.PlanRef, RecordRef: event.RecordRef, Observed: event.Observed,
		LevelResults:   []LevelResultV1{levelResultV1ForTest(5, 1, LevelResultUnavailable, strings.Repeat("e", 64))},
		EvaluationTime: event.EvaluationTime, DetectPlanFingerprint: event.DetectPlanFingerprint,
		TriggerStateFingerprint: event.TriggerStateFingerprint, ExecutionID: "execution-1", MaxEvidenceBytes: 4096,
	}
	if _, err := BuildTriggerEventV1(bad); err == nil {
		t.Fatal("BuildTriggerEventV1() accepted unavailable LevelResult")
	}
}

func TestDecodeTriggerEventV1EnforcesReaderBudgets(t *testing.T) {
	t.Parallel()

	payload, err := os.ReadFile("testdata/go-v2/trigger_event_v1.json")
	if err != nil {
		t.Fatalf("os.ReadFile(trigger event) error = %v", err)
	}
	limits := TriggerEventReaderLimitsV1{MaxPayloadBytes: len(payload), MaxEvidenceBytes: len(payload)}
	if _, err := DecodeTriggerEventV1WithLimits(payload, limits); err != nil {
		t.Fatalf("DecodeTriggerEventV1WithLimits(exact payload budget) error = %v", err)
	}
	limits.MaxPayloadBytes--
	if _, err := DecodeTriggerEventV1WithLimits(payload, limits); err == nil {
		t.Fatal("DecodeTriggerEventV1WithLimits() accepted payload above configured budget")
	}
	limits = TriggerEventReaderLimitsV1{MaxPayloadBytes: len(payload), MaxEvidenceBytes: 1}
	if _, err := DecodeTriggerEventV1WithLimits(payload, limits); err == nil {
		t.Fatal("DecodeTriggerEventV1WithLimits() accepted evidence above configured budget")
	}
}

func TestLevelResultV1RequiresConsistentWindowDecision(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(*LevelResultV1)
		wantErr bool
	}{
		{name: "anomalous point below trigger remains normal"},
		{
			name: "normal point remains normal while historical window satisfies trigger",
			mutate: func(result *LevelResultV1) {
				result.DetectEvidence.DetectionResult = "NORMAL"
				result.DecisionWindow.Trigger.ObservedAnomalies = 2
			},
		},
		{
			name: "anomalous point with satisfied trigger becomes abnormal",
			mutate: func(result *LevelResultV1) {
				result.Result = LevelResultAbnormal
				result.DecisionWindow.Trigger.ObservedAnomalies = 2
			},
		},
		{
			name: "normal point cannot become abnormal from historical window alone",
			mutate: func(result *LevelResultV1) {
				result.DetectEvidence.DetectionResult = "NORMAL"
				result.Result = LevelResultAbnormal
				result.DecisionWindow.Trigger.ObservedAnomalies = 2
			},
			wantErr: true,
		},
		{
			name: "anomalous point cannot remain normal when trigger is satisfied",
			mutate: func(result *LevelResultV1) {
				result.DecisionWindow.Trigger.ObservedAnomalies = 2
			},
			wantErr: true,
		},
		{
			name: "anomalous point may recover when trigger is false and recovery is true",
			mutate: func(result *LevelResultV1) {
				result.Result = LevelResultRecovery
				result.DecisionWindow.Recovery.ObservedConsecutiveMisses = 1
			},
		},
		{
			name: "required anomalies exceed window",
			mutate: func(result *LevelResultV1) {
				result.DecisionWindow.Trigger.RequiredAnomalies = 3
			},
		},
		{
			name: "abnormal without satisfied trigger",
			mutate: func(result *LevelResultV1) {
				result.Result = LevelResultAbnormal
			},
			wantErr: true,
		},
		{
			name: "normal while recovery is satisfied",
			mutate: func(result *LevelResultV1) {
				result.DecisionWindow.Recovery.ObservedConsecutiveMisses = 1
			},
			wantErr: true,
		},
		{
			name: "recovery while trigger is satisfied",
			mutate: func(result *LevelResultV1) {
				result.Result = LevelResultRecovery
				result.DecisionWindow.Trigger.ObservedAnomalies = 2
				result.DecisionWindow.Recovery.ObservedConsecutiveMisses = 1
			},
			wantErr: true,
		},
		{
			name: "warming permits monotonic abnormal",
			mutate: func(result *LevelResultV1) {
				result.Result = LevelResultAbnormal
				result.DecisionWindow.Trigger.ObservedAnomalies = 2
				result.DecisionWindow.HistoryCompleteness = "WARMING"
			},
		},
		{
			name: "warming rejects normal",
			mutate: func(result *LevelResultV1) {
				result.DecisionWindow.HistoryCompleteness = "WARMING"
			},
			wantErr: true,
		},
		{
			name: "gapped rejects recovery",
			mutate: func(result *LevelResultV1) {
				result.Result = LevelResultRecovery
				result.DecisionWindow.Recovery.ObservedConsecutiveMisses = 1
				result.DecisionWindow.HistoryCompleteness = "GAPPED"
			},
			wantErr: true,
		},
		{
			name: "unknown history completeness",
			mutate: func(result *LevelResultV1) {
				result.DecisionWindow.HistoryCompleteness = "UNKNOWN"
			},
			wantErr: true,
		},
		{
			name: "window end must equal source time",
			mutate: func(result *LevelResultV1) {
				result.DecisionWindow.Trigger.WindowEnd--
			},
			wantErr: true,
		},
		{
			name: "recovery oldest window cannot be in future",
			mutate: func(result *LevelResultV1) {
				result.DecisionWindow.Recovery.OldestWindowStart = result.DecisionWindow.SourceTime + 1
			},
			wantErr: true,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result := levelResultV1ForTest(5, 1, LevelResultNormal, strings.Repeat("e", 64))
			result.DecisionWindow.Trigger.WindowSize = 2
			result.DecisionWindow.Trigger.RequiredAnomalies = 2
			result.DecisionWindow.Trigger.ObservedAnomalies = 1
			if test.mutate != nil {
				test.mutate(&result)
			}
			err := validateSuccessfulLevelResultsV1([]LevelResultV1{result})
			if (err != nil) != test.wantErr {
				t.Fatalf("validateSuccessfulLevelResultsV1() error = %v, wantErr=%t", err, test.wantErr)
			}
		})
	}
}

func TestReceiptAndSummaryStrictRoundTrip(t *testing.T) {
	t.Parallel()

	receipt, err := BuildMessageReceiptV1(MessageReceiptV1{
		ExecutionID: "execution-1", MessageID: "message-1", PayloadDigest: strings.Repeat("1", 64),
		PlanSetDigest: strings.Repeat("2", 64), SourceWindow: SourceWindowV2{FromTime: 1, UntilTime: 2},
		Status:  ReceiptStatusCompleted,
		Counts:  ReceiptCountsV1{Received: 1, Selected: 1, Processed: 1},
		PerPlan: []PlanReceiptV1{{PlanID: "1001", Selected: 1, Normal: 1}},
	})
	if err != nil {
		t.Fatalf("BuildMessageReceiptV1() error = %v", err)
	}
	if got, want := receipt.ReceiptID, "2032ccba8166552f363083f774baf09b8936ad14c4ae1623ebb52eb85220f024"; got != want {
		t.Fatalf("receipt id = %s, want %s", got, want)
	}
	payload, err := EncodeMessageReceiptV1(receipt)
	if err != nil {
		t.Fatalf("EncodeMessageReceiptV1() error = %v", err)
	}
	assertGoldenPayloadV2(t, "testdata/go-v2/message_receipt_v1.json", payload)
	if _, err := DecodeMessageReceiptV1(payload); err != nil {
		t.Fatalf("DecodeMessageReceiptV1() error = %v", err)
	}
	unknown := append(payload[:len(payload)-1], []byte(`,"future":true}`)...)
	if _, err := DecodeMessageReceiptV1(unknown); err == nil {
		t.Fatal("DecodeMessageReceiptV1() accepted an unknown 1.0 field")
	}
	var legacy map[string]any
	if err := json.Unmarshal(payload, &legacy); err != nil {
		t.Fatalf("decode receipt for legacy-field test: %v", err)
	}
	legacyPerPlan := legacy["per_plan"].([]any)
	legacyPerPlan[0].(map[string]any)["result_identity_digest"] = strings.Repeat("3", 64)
	legacyPayload, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("encode receipt with legacy field: %v", err)
	}
	if _, err := DecodeMessageReceiptV1(legacyPayload); err == nil {
		t.Fatal("DecodeMessageReceiptV1() accepted removed result_identity_digest field")
	}

	summary, err := BuildExecutionSummaryV1(ExecutionSummaryV1{
		ExecutionID: "execution-1", TenantID: "default", QueryGroupKey: "query-group-1",
		SourceWindow: SourceWindowV2{FromTime: 1, UntilTime: 2}, PlanSetDigest: strings.Repeat("2", 64),
		Source: CountSetV1{Messages: 1, Records: 1, Bytes: 10}, Published: CountSetV1{Messages: 1, Records: 1, Bytes: 10},
	})
	if err != nil {
		t.Fatalf("BuildExecutionSummaryV1() error = %v", err)
	}
	if got, want := summary.SummaryID, "fdd0691b2822feb1f816a33c203f009a682626de9cf8853ccd68a102e22631b4"; got != want {
		t.Fatalf("summary id = %s, want %s", got, want)
	}
	summaryPayload, err := EncodeExecutionSummaryV1(summary)
	if err != nil {
		t.Fatalf("EncodeExecutionSummaryV1() error = %v", err)
	}
	assertGoldenPayloadV2(t, "testdata/go-v2/execution_summary_v1.json", summaryPayload)
	if _, err := DecodeExecutionSummaryV1(summaryPayload); err != nil {
		t.Fatalf("DecodeExecutionSummaryV1() error = %v", err)
	}
}

func TestMessageReceiptV1CountModel(t *testing.T) {
	t.Parallel()

	valid := validMessageReceiptV1ForTest()
	valid.Counts = ReceiptCountsV1{Received: 2, Selected: 3, Processed: 2, Unavailable: 1, Events: 1}
	valid.PerPlan = []PlanReceiptV1{
		{PlanID: "1001", Selected: 2, Abnormal: 1, Unavailable: 1},
		{PlanID: "1002", Selected: 1, Normal: 1},
	}
	valid.ReasonCounts = []ReasonCountV1{{ReasonCode: ReasonRequiredValueMissing, Count: 1}}
	if _, err := BuildMessageReceiptV1(valid); err != nil {
		t.Fatalf("BuildMessageReceiptV1(valid selected > received) error = %v", err)
	}
	mixedLevel := valid
	mixedLevel.Status = ReceiptStatusCompletedWithTerminal
	mixedLevel.Counts.LevelTerminalAffected = 1
	mixedLevel.PerPlan = append([]PlanReceiptV1(nil), valid.PerPlan...)
	mixedLevel.PerPlan[0].LevelTerminalAffected = 1
	if _, err := BuildMessageReceiptV1(mixedLevel); err != nil {
		t.Fatalf("BuildMessageReceiptV1(valid mixed-Level terminal) error = %v", err)
	}
	for _, reason := range []string{ReasonSelectorInvalid, ReasonPlanInvalid, ReasonLevelInvalid, ReasonValidationBudgetExceeded} {
		uncountedTerminal := validMessageReceiptV1ForTest()
		uncountedTerminal.Status = ReceiptStatusCompletedWithTerminal
		uncountedTerminal.ReasonCounts = []ReasonCountV1{{ReasonCode: reason, Count: 1}}
		if _, err := BuildMessageReceiptV1(uncountedTerminal); err != nil {
			t.Fatalf("BuildMessageReceiptV1(valid uncounted %s terminal fact) error = %v", reason, err)
		}
	}
	affectedWithoutDecision := validMessageReceiptV1ForTest()
	affectedWithoutDecision.Status = ReceiptStatusCompletedWithTerminal
	affectedWithoutDecision.Counts = ReceiptCountsV1{
		Received: 1, Selected: 1, Unavailable: 1, LevelTerminalAffected: 1,
	}
	affectedWithoutDecision.PerPlan[0].Normal = 0
	affectedWithoutDecision.PerPlan[0].Unavailable = 1
	affectedWithoutDecision.PerPlan[0].LevelTerminalAffected = 1
	if _, err := BuildMessageReceiptV1(affectedWithoutDecision); err == nil {
		t.Fatal("BuildMessageReceiptV1() accepted Level terminal affected without a valid three-state result")
	}

	rejected := validMessageReceiptV1ForTest()
	rejected.Status = ReceiptStatusRejected
	rejected.Counts = ReceiptCountsV1{}
	rejected.PerPlan = []PlanReceiptV1{}
	rejected.ReasonCounts = []ReasonCountV1{{ReasonCode: ReasonMalformedJSON, Count: 1}}
	if _, err := BuildMessageReceiptV1(rejected); err != nil {
		t.Fatalf("BuildMessageReceiptV1(valid rejected) error = %v", err)
	}
	rejected.Counts.LevelTerminalAffected = 1
	if _, err := BuildMessageReceiptV1(rejected); err == nil {
		t.Fatal("BuildMessageReceiptV1() accepted REJECTED with a level terminal affected count")
	}

	tests := []struct {
		name   string
		mutate func(*MessageReceiptV1)
	}{
		{name: "top selected decomposition", mutate: func(receipt *MessageReceiptV1) { receipt.Counts.Selected++ }},
		{name: "per plan decomposition", mutate: func(receipt *MessageReceiptV1) { receipt.PerPlan[0].Selected++ }},
		{name: "per plan selected sum", mutate: func(receipt *MessageReceiptV1) { receipt.PerPlan[1].Selected++ }},
		{name: "per plan selected exceeds received", mutate: func(receipt *MessageReceiptV1) { receipt.Counts.Received = 1 }},
		{name: "processed sum", mutate: func(receipt *MessageReceiptV1) { receipt.Counts.Processed++ }},
		{name: "events sum", mutate: func(receipt *MessageReceiptV1) { receipt.Counts.Events++ }},
		{name: "level terminal affected sum", mutate: func(receipt *MessageReceiptV1) { receipt.Counts.LevelTerminalAffected++ }},
		{name: "level terminal affected exceeds selected", mutate: func(receipt *MessageReceiptV1) {
			receipt.Status = ReceiptStatusCompletedWithTerminal
			receipt.Counts.LevelTerminalAffected = receipt.PerPlan[0].Selected + 1
			receipt.PerPlan[0].LevelTerminalAffected = receipt.PerPlan[0].Selected + 1
		}},
		{name: "completed status with level terminal affected", mutate: func(receipt *MessageReceiptV1) {
			receipt.Counts.LevelTerminalAffected = 1
			receipt.PerPlan[0].LevelTerminalAffected = 1
		}},
		{name: "completed with terminal status without terminal counts", mutate: func(receipt *MessageReceiptV1) {
			receipt.Status = ReceiptStatusCompletedWithTerminal
		}},
		{name: "terminal status", mutate: func(receipt *MessageReceiptV1) {
			receipt.Counts.Terminal = 1
			receipt.Counts.Selected++
			receipt.PerPlan[1].Terminal = 1
			receipt.PerPlan[1].Selected++
		}},
		{name: "rejected business counts", mutate: func(receipt *MessageReceiptV1) {
			receipt.Status = ReceiptStatusRejected
		}},
		{name: "rejected per plan", mutate: func(receipt *MessageReceiptV1) {
			receipt.Status = ReceiptStatusRejected
			receipt.Counts = ReceiptCountsV1{}
		}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			receipt := valid
			receipt.PerPlan = append([]PlanReceiptV1(nil), valid.PerPlan...)
			receipt.ReasonCounts = append([]ReasonCountV1(nil), valid.ReasonCounts...)
			test.mutate(&receipt)
			if _, err := BuildMessageReceiptV1(receipt); err == nil {
				t.Fatal("BuildMessageReceiptV1() accepted inconsistent counts")
			}
		})
	}
}

func TestMessageReceiptV1RepresentsMixedLevelTerminal(t *testing.T) {
	t.Parallel()

	receipt := validMessageReceiptV1ForTest()
	receipt.Status = ReceiptStatusCompletedWithTerminal
	receipt.Counts = ReceiptCountsV1{
		Received: 1, Selected: 1, Processed: 1, LevelTerminalAffected: 1, Events: 1,
	}
	receipt.PerPlan[0].Normal = 0
	receipt.PerPlan[0].Abnormal = 1
	receipt.PerPlan[0].LevelTerminalAffected = 1
	built, err := BuildMessageReceiptV1(receipt)
	if err != nil {
		t.Fatalf("BuildMessageReceiptV1() error = %v", err)
	}
	payload, err := EncodeMessageReceiptV1(built)
	if err != nil {
		t.Fatalf("EncodeMessageReceiptV1() error = %v", err)
	}
	assertGoldenPayloadV2(t, "testdata/go-v2/message_receipt_mixed_level_v1.json", payload)

	decoded, err := DecodeMessageReceiptV1(payload)
	if err != nil {
		t.Fatalf("DecodeMessageReceiptV1() cannot express a processed Plan x Record with a sibling Level terminal: %v", err)
	}
	if decoded.Status != ReceiptStatusCompletedWithTerminal || decoded.Counts.Selected != 1 || decoded.Counts.Processed != 1 ||
		decoded.Counts.Terminal != 0 || decoded.Counts.LevelTerminalAffected != 1 || decoded.Counts.Events != 1 ||
		len(decoded.PerPlan) != 1 || decoded.PerPlan[0].Selected != 1 || decoded.PerPlan[0].Abnormal != 1 ||
		decoded.PerPlan[0].Terminal != 0 || decoded.PerPlan[0].LevelTerminalAffected != 1 {
		t.Fatalf("mixed-Level receipt = %#v", decoded)
	}
}

func TestMessageReceiptV1RequiresLevelTerminalAffectedShape(t *testing.T) {
	t.Parallel()

	receipt, err := BuildMessageReceiptV1(validMessageReceiptV1ForTest())
	if err != nil {
		t.Fatalf("BuildMessageReceiptV1() error = %v", err)
	}
	payload, err := EncodeMessageReceiptV1(receipt)
	if err != nil {
		t.Fatalf("EncodeMessageReceiptV1() error = %v", err)
	}

	for _, scope := range []string{"counts", "per_plan"} {
		t.Run(scope, func(t *testing.T) {
			var object map[string]json.RawMessage
			if err := json.Unmarshal(payload, &object); err != nil {
				t.Fatalf("decode receipt fixture: %v", err)
			}
			switch scope {
			case "counts":
				var counts map[string]json.RawMessage
				if err := json.Unmarshal(object["counts"], &counts); err != nil {
					t.Fatalf("decode counts fixture: %v", err)
				}
				delete(counts, "level_terminal_affected")
				object["counts"], err = json.Marshal(counts)
			case "per_plan":
				var perPlan []map[string]json.RawMessage
				if err := json.Unmarshal(object["per_plan"], &perPlan); err != nil {
					t.Fatalf("decode per_plan fixture: %v", err)
				}
				delete(perPlan[0], "level_terminal_affected")
				object["per_plan"], err = json.Marshal(perPlan)
			}
			if err != nil {
				t.Fatalf("encode %s fixture: %v", scope, err)
			}
			missing, err := json.Marshal(object)
			if err != nil {
				t.Fatalf("encode receipt fixture: %v", err)
			}
			if _, err := DecodeMessageReceiptV1(missing); err == nil {
				t.Fatalf("DecodeMessageReceiptV1() accepted missing %s level_terminal_affected", scope)
			}
		})
	}
}

func TestExecutionSummaryV1CountModel(t *testing.T) {
	t.Parallel()

	valid := validExecutionSummaryV1ForTest()
	valid.Source = CountSetV1{Messages: 2, Records: 3, Bytes: 30}
	valid.Published = CountSetV1{Messages: 1, Records: 2, Bytes: 20}
	valid.Dropped = CountSetV1{Messages: 1, Records: 1, Bytes: 10}
	valid.ReasonCounts = []ReasonCountV1{{ReasonCode: ReasonRecordTooLarge, Count: 1}}
	if _, err := BuildExecutionSummaryV1(valid); err != nil {
		t.Fatalf("BuildExecutionSummaryV1(valid partial drop) error = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*ExecutionSummaryV1)
	}{
		{name: "messages", mutate: func(summary *ExecutionSummaryV1) { summary.Source.Messages++ }},
		{name: "records", mutate: func(summary *ExecutionSummaryV1) { summary.Source.Records++ }},
		{name: "bytes", mutate: func(summary *ExecutionSummaryV1) { summary.Source.Bytes++ }},
		{name: "drop without reason", mutate: func(summary *ExecutionSummaryV1) { summary.ReasonCounts = []ReasonCountV1{} }},
		{name: "reason without drop", mutate: func(summary *ExecutionSummaryV1) {
			summary.Source = summary.Published
			summary.Dropped = CountSetV1{}
		}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			summary := valid
			summary.ReasonCounts = append([]ReasonCountV1(nil), valid.ReasonCounts...)
			test.mutate(&summary)
			if _, err := BuildExecutionSummaryV1(summary); err == nil {
				t.Fatal("BuildExecutionSummaryV1() accepted inconsistent counts")
			}
		})
	}
}

func validExecutionEnvelopeV2(t *testing.T) *ExecutionEnvelopeV2 {
	t.Helper()

	fields := []DimensionFieldV2{{Name: "host", Value: json.RawMessage(`"127.0.0.1"`)}}
	dimensionDigest, err := DeriveDimensionIdentityDigestV2("default", "2", fields)
	if err != nil {
		t.Fatalf("DeriveDimensionIdentityDigestV2() error = %v", err)
	}
	recordID, err := DeriveRecordIDV2(dimensionDigest, 1_725_000_000)
	if err != nil {
		t.Fatalf("DeriveRecordIDV2() error = %v", err)
	}

	thresholdConfig := json.RawMessage(`{"value_field":"value","data_unit":"percent","threshold_unit_prefix":"","precision":{"decimal_places":6,"rounding":"HALF_EVEN"},"groups":[{"conditions":[{"operator":"GT","threshold_decimal":"50"}]}]}`)
	level := func(id, priority uint32) LevelIRV2 {
		return LevelIRV2{
			Definition: LevelDefinitionV2{LevelID: id, Priority: priority}, Connector: LevelConnectorAND,
			DetectPlan:   DetectPlanV2{Algorithms: []AlgorithmIRV2{{Type: "Threshold", Version: 1, Config: thresholdConfig}}},
			TriggerPlan:  TypedPlanV1{Type: "N_OF_M", Version: 1, Config: json.RawMessage(`{"window_size":1,"required_anomalies":1,"step_seconds":60}`)},
			RecoveryPlan: TypedPlanV1{Type: "CONTINUOUS_TRIGGER_MISS", Version: 1, Config: json.RawMessage(`{"enabled":true,"consecutive_windows":1}`)},
		}
	}
	strategyRef := StrategyRefV2{TenantID: "default", StrategyID: "1001", Revision: "strategy-r1"}
	strategyIR := StrategyIRV2{
		Schema: Schema{Name: StrategyIRSchemaV2, Major: 2, Minor: 0}, RequiredFeatures: []string{}, StrategyRef: strategyRef,
		ExecutionSemantics: ExecutionSemanticsV2{
			EvaluationScope: EvaluationScopeSeries, QueryWindow: 300, AggregationInterval: 60, EvaluationInterval: 60,
			LatenessTolerance: 120,
		},
		InputProjection: InputProjectionV2{
			ValueFields: []string{"value"}, DimensionFields: []string{"host"}, BusinessIdentityField: "bk_biz_id",
			MultiValueAlignment: "SINGLE_VALUE", DataUnit: "percent", MissingValuePolicy: MissingValuePolicyRequired,
		},
		Levels: []LevelIRV2{level(1, 20), level(5, 1)},
	}
	plan := EvaluationPlanV2{
		PlanID: "1001", StrategyRef: strategyRef, InputProjection: strategyIR.InputProjection,
		StrategyIR: strategyIR,
	}
	ranges := []SelectorRangeV2{{Start: 0, End: 1}}
	envelope := &ExecutionEnvelopeV2{
		Schema: Schema{Name: ExecutionEnvelopeSchemaV2, Major: 2, Minor: 0}, RequiredFeatures: []string{},
		ExecutionID: "execution-1", MessageID: "message-1", TenantID: "default",
		QueryGroup:   QueryGroupV2{Key: "query-group-1", QueryMD5: "query-md5-1", QueryRevision: "query-r1", EvaluationTime: 1_725_000_060},
		SourceWindow: SourceWindowV2{FromTime: 1_724_999_700, UntilTime: 1_725_000_060},
		QueryResult:  QueryResultV2{Completeness: QueryCompletenessFull},
		DatasetContract: DatasetContractV2{
			SchemaDigest: strings.Repeat("1", 64), NormalizationDigest: strings.Repeat("2", 64), IdentityFields: []string{"host"},
			SourceTimeField: "time", ReceivedTimeField: "received_time",
		},
		PlanSet:   PlanSetV2{PlanCount: 1, EvaluationPlans: []EvaluationPlanV2{plan}},
		Selectors: []PlanSelectorV2{{PlanOrdinal: 0, Selector: SelectorV2{Kind: SelectorKindRanges, Ranges: &ranges}}},
		Records: []CanonicalRecordV2{{
			RecordID: recordID, SourceTime: 1_725_000_000, BusinessID: "2",
			DimensionIdentity: DimensionIdentityV2{Fields: fields, Digest: dimensionDigest},
			Values:            map[string]json.RawMessage{"value": json.RawMessage(`50.1`)},
			Dimensions:        map[string]json.RawMessage{"host": json.RawMessage(`"127.0.0.1"`)}, ReceivedTime: 1_725_000_001,
		}},
	}
	return envelope
}

func encodeExecutionEnvelopeV2ForTest(t *testing.T, envelope *ExecutionEnvelopeV2) []byte {
	t.Helper()

	planDigest, err := DerivePlanSetDigestV2(envelope.PlanSet)
	if err != nil {
		t.Fatalf("DerivePlanSetDigestV2() error = %v", err)
	}
	envelope.PlanSet.PlanSetDigest = planDigest
	payloadDigest, err := DeriveExecutionEnvelopePayloadDigestV2(*envelope)
	if err != nil {
		t.Fatalf("DeriveExecutionEnvelopePayloadDigestV2() error = %v", err)
	}
	envelope.PayloadDigest = payloadDigest
	payload, err := CanonicalJSONV2(envelope)
	if err != nil {
		t.Fatalf("CanonicalJSONV2(envelope) error = %v", err)
	}
	return payload
}

func recomputeEnvelopeDigestsForRawTest(t *testing.T, object map[string]json.RawMessage) []byte {
	t.Helper()

	var planSet map[string]json.RawMessage
	if err := json.Unmarshal(object["plan_set"], &planSet); err != nil {
		t.Fatalf("json.Unmarshal(plan_set) error = %v", err)
	}
	planSetDigest, err := digestCanonicalV2("plan_set", "plan-set-v2", mapWithoutKeyV2(planSet, "plan_set_digest"))
	if err != nil {
		t.Fatalf("digest Plan Set error = %v", err)
	}
	encodedPlanDigest, _ := json.Marshal(planSetDigest)
	planSet["plan_set_digest"] = encodedPlanDigest
	planSetPayload, err := CanonicalJSONV2(planSet)
	if err != nil {
		t.Fatalf("CanonicalJSONV2(plan_set) error = %v", err)
	}
	object["plan_set"] = planSetPayload
	payloadDigest, err := digestCanonicalV2("payload", "execution-envelope-payload-v2", mapWithoutKeyV2(object, "payload_digest"))
	if err != nil {
		t.Fatalf("digest Envelope error = %v", err)
	}
	encodedPayloadDigest, _ := json.Marshal(payloadDigest)
	object["payload_digest"] = encodedPayloadDigest
	payload, err := CanonicalJSONV2(object)
	if err != nil {
		t.Fatalf("CanonicalJSONV2(envelope map) error = %v", err)
	}
	return payload
}

func mapWithoutKeyV2(source map[string]json.RawMessage, omitted string) map[string]json.RawMessage {
	copy := make(map[string]json.RawMessage, len(source)-1)
	for key, value := range source {
		if key != omitted {
			copy[key] = value
		}
	}
	return copy
}

func generousReaderLimitsV2() ReaderLimitsV2 {
	return ReaderLimitsV2{
		MaxEnvelopeBytes: 1 << 20, MaxRecordsPerMessage: 100, MaxPlansPerMessage: 10, MaxLevelsPerPlan: 10,
		MaxSelectorBytes: 1 << 16, MaxRecordBytes: 1 << 16, MaxPlanSetBytes: 1 << 18,
		MaxContractDepth: 32, MaxStringBytes: 1 << 16, MaxValidationIssues: 100,
	}
}

func containsIssue(issues []ValidationIssue, scope ValidationScope, reason string) bool {
	for _, issue := range issues {
		if issue.Scope == scope && issue.ReasonCode == reason {
			return true
		}
	}
	return false
}

func hasOnlyPlanOrdinalsV2(issues []ValidationIssue, ordinals ...uint32) bool {
	if len(issues) != len(ordinals) {
		return false
	}
	for index, ordinal := range ordinals {
		if issues[index].PlanOrdinal == nil || *issues[index].PlanOrdinal != ordinal {
			return false
		}
	}
	return true
}

func containsLevelIssueV2(issues []ValidationIssue, levelID uint32) bool {
	for _, issue := range issues {
		if issue.Scope == ValidationScopeLevel && issue.LevelID != nil && *issue.LevelID == levelID {
			return true
		}
	}
	return false
}

func containsRecordIssueV2(issues []ValidationIssue, ordinal uint32, reason string) bool {
	for _, issue := range issues {
		if issue.Scope == ValidationScopeRecord && issue.RecordOrdinal != nil && *issue.RecordOrdinal == ordinal && issue.ReasonCode == reason {
			return true
		}
	}
	return false
}

func equalUint32s(left, right []uint32) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func assertGoldenPayloadV2(t *testing.T, path string, got []byte) {
	t.Helper()
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile(%s) error = %v", path, err)
	}
	if !bytes.Equal(got, bytes.TrimSpace(want)) {
		t.Fatalf("payload differs from %s\ngot:  %s\nwant: %s", path, got, bytes.TrimSpace(want))
	}
}

func levelResultV1ForTest(levelID, priority uint32, result, fingerprint string) LevelResultV1 {
	observedAnomalies := uint32(0)
	observedMisses := uint32(0)
	if result == LevelResultAbnormal {
		observedAnomalies = 1
	}
	if result == LevelResultRecovery {
		observedMisses = 1
	}
	return LevelResultV1{
		LevelID: levelID, Priority: priority, Result: result, LevelTriggerFingerprint: fingerprint,
		DecisionWindow: DecisionWindowV1{
			Type: "N_OF_M_WITH_CONTINUOUS_MISS", Version: 1, SourceTime: 1_725_000_000,
			Trigger: TriggerWindowEvidenceV1{
				WindowStart: 1_725_000_000, WindowEnd: 1_725_000_000, WindowSize: 1,
				RequiredAnomalies: 1, ObservedAnomalies: observedAnomalies,
			},
			Recovery: RecoveryWindowEvidenceV1{
				Enabled: true, RequiredConsecutiveWindows: 1, ObservedConsecutiveMisses: observedMisses,
				OldestWindowStart: 1_725_000_000,
			},
			HistoryCompleteness: "FULL", WindowEvidence: WindowEvidenceV1{AnomalyTimestampsDigest: strings.Repeat("9", 64)},
		},
		DetectEvidence: DetectEvidenceV1{
			DetectionResult: "ANOMALOUS", PredicateDigest: strings.Repeat("8", 64),
			NormalizedValue: json.RawMessage(`50.1`), EffectiveTimeStatus: "ACTIVE",
		},
	}
}

func validMessageReceiptV1ForTest() MessageReceiptV1 {
	return MessageReceiptV1{
		ExecutionID: "execution-1", MessageID: "message-1", PayloadDigest: strings.Repeat("1", 64),
		PlanSetDigest: strings.Repeat("2", 64), SourceWindow: SourceWindowV2{FromTime: 1, UntilTime: 2},
		Status:  ReceiptStatusCompleted,
		Counts:  ReceiptCountsV1{Received: 1, Selected: 1, Processed: 1},
		PerPlan: []PlanReceiptV1{{PlanID: "1001", Selected: 1, Normal: 1}},
	}
}

func validExecutionSummaryV1ForTest() ExecutionSummaryV1 {
	return ExecutionSummaryV1{
		ExecutionID: "execution-1", TenantID: "default", QueryGroupKey: "query-group-1",
		SourceWindow: SourceWindowV2{FromTime: 1, UntilTime: 2}, PlanSetDigest: strings.Repeat("2", 64),
		Source: CountSetV1{Messages: 1, Records: 1, Bytes: 10}, Published: CountSetV1{Messages: 1, Records: 1, Bytes: 10},
	}
}
