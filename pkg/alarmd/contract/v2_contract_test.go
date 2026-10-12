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
	blockedExactSet, ok := LookupReasonV2(ReasonBlockedExactSetUnavailable)
	if !ok || blockedExactSet.Class != ReasonClassDeterministic || blockedExactSet.Domains != ReasonDomainObservation {
		t.Fatalf("Blocked exact-set reason definition = (%#v, %t)", blockedExactSet, ok)
	}
	// A deterministic BeginSlot failure is named by its own observation-only
	// reason so it is never confused with an exact-set or transport condition.
	beginFailed, ok := LookupReasonV2(ReasonProgressBeginFailed)
	if !ok || beginFailed.Class != ReasonClassDeterministic || beginFailed.Domains != ReasonDomainObservation ||
		ReasonAllowedForV2(ReasonProgressBeginFailed, ReasonDomainReceipt) || ReasonAllowedForV2(ReasonProgressBeginFailed, ReasonDomainQueryResult) {
		t.Fatalf("Progress begin failed reason definition = (%#v, %t)", beginFailed, ok)
	}
	// PROVIDER_UNAVAILABLE used to stand in for every retryable control
	// condition; these split it by cause. They are observation-only: they
	// appear on non-committed Retrying results and are never persisted or
	// carried by receipts.
	for _, reason := range []string{
		ReasonProgressBeginRejected, ReasonActivationReadFailed, ReasonSnapshotRetryPending, ReasonSlotSourceRetry,
	} {
		definition, ok := LookupReasonV2(reason)
		if !ok || definition.Class != ReasonClassRetryable || definition.Domains != ReasonDomainObservation ||
			ReasonAllowedForV2(reason, ReasonDomainReceipt) || ReasonAllowedForV2(reason, ReasonDomainQueryResult) {
			t.Fatalf("split provider reason %q definition = (%#v, %t)", reason, definition, ok)
		}
	}
	snapshotUnavailable, ok := LookupReasonV2(ReasonSnapshotUnavailable)
	if !ok || snapshotUnavailable.Class != ReasonClassCoverage ||
		snapshotUnavailable.Domains != ReasonDomainObservation ||
		ReasonAllowedForV2(ReasonSnapshotUnavailable, ReasonDomainReceipt) ||
		ReasonAllowedForV2(ReasonSnapshotUnavailable, ReasonDomainQueryResult) {
		t.Fatalf("Snapshot unavailable reason definition = (%#v, %t)", snapshotUnavailable, ok)
	}
	gapSkipped, ok := LookupReasonV2(ReasonGapSkipped)
	if !ok || gapSkipped.Class != ReasonClassCoverage || gapSkipped.Domains != ReasonDomainObservation ||
		ReasonAllowedForV2(ReasonGapSkipped, ReasonDomainReceipt) ||
		ReasonAllowedForV2(ReasonGapSkipped, ReasonDomainQueryResult) {
		t.Fatalf("Gap-skipped reason definition = (%#v, %t)", gapSkipped, ok)
	}
	schedulePruned, ok := LookupReasonV2(ReasonSchedulePruned)
	if !ok || schedulePruned.Class != ReasonClassCoverage || schedulePruned.Domains != ReasonDomainObservation ||
		ReasonAllowedForV2(ReasonSchedulePruned, ReasonDomainReceipt) ||
		ReasonAllowedForV2(ReasonSchedulePruned, ReasonDomainQueryResult) {
		t.Fatalf("Schedule-pruned reason definition = (%#v, %t)", schedulePruned, ok)
	}
	if !ReasonAllowedForV2(ReasonQueryPartial, ReasonDomainQueryResult) ||
		ReasonAllowedForV2(ReasonRecordInvalid, ReasonDomainQueryResult) {
		t.Fatal("QueryResult Reason domain accepted an invalid mapping")
	}
	readinessBudget, ok := LookupReasonV2(ReasonReadinessBudgetInvalid)
	if !ok || readinessBudget.Class != ReasonClassCoverage || readinessBudget.Domains != reasonQueryDomainsV2 ||
		!ReasonAllowedForV2(ReasonReadinessBudgetInvalid, ReasonDomainQueryResult) ||
		!ReasonAllowedForV2(ReasonReadinessBudgetInvalid, ReasonDomainReceipt) {
		t.Fatalf("readiness budget reason definition = (%#v, %t)", readinessBudget, ok)
	}
	executionBudget, ok := LookupReasonV2(ReasonExecutionBudgetExhausted)
	if !ok || executionBudget.Class != ReasonClassCoverage ||
		executionBudget.Domains != ReasonDomainQueryResult|ReasonDomainReceipt|ReasonDomainObservation ||
		!ReasonAllowedForV2(ReasonExecutionBudgetExhausted, ReasonDomainQueryResult) ||
		!LevelUnavailableReasonV2(ReasonExecutionBudgetExhausted) {
		t.Fatalf("execution budget reason definition = (%#v, %t)", executionBudget, ok)
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
	for _, reason := range []string{ReasonStateCorrupt, ReasonStateSchemaUnsupported, ReasonStateBudgetExceeded} {
		definition, ok := LookupReasonV2(reason)
		if !ok || definition.Class != ReasonClassDeterministic ||
			!definition.Domains.Has(ReasonDomainReceipt) || !definition.Domains.Has(ReasonDomainObservation) {
			t.Fatalf("state deterministic reason %q definition = (%#v, %t)", reason, definition, ok)
		}
	}
	firstCode := catalog[0].Code
	catalog[0].Code = "MUTATED"
	if refreshed := ReasonCatalogV2(); len(refreshed) == 0 || refreshed[0].Code != firstCode {
		t.Fatal("ReasonCatalogV2 exposed mutable catalog storage")
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
			// decision-022 section 9.1: an incomplete window may close what it
			// opened. It used to refuse this outright, which left an alert
			// opened on a short window open until the window filled - never,
			// for a strategy whose window outlasts the interval between
			// releases.
			name: "gapped permits recovery with the evidence for it",
			mutate: func(result *LevelResultV1) {
				result.Result = LevelResultRecovery
				result.DecisionWindow.Recovery.Enabled = true
				result.DecisionWindow.Recovery.RequiredConsecutiveWindows = 2
				result.DecisionWindow.Recovery.ObservedConsecutiveMisses = 2
				result.DecisionWindow.HistoryCompleteness = "GAPPED"
			},
			wantErr: false,
		},
		{
			// One short of it, and nothing else different. Without this the
			// rule above reads as "any incomplete window may claim recovery".
			name: "gapped rejects recovery one window short of its evidence",
			mutate: func(result *LevelResultV1) {
				result.Result = LevelResultRecovery
				result.DecisionWindow.Recovery.Enabled = true
				result.DecisionWindow.Recovery.RequiredConsecutiveWindows = 2
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
