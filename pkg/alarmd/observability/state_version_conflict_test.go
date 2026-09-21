// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// The refused chunk's line carries the count of each kind and the first
// sample of each, so a reader sees "2 missing, 1 moved: series X expected 7
// found nothing" without a second query; a store that did not say which
// comparison refused lands on other, never on a kind a reader would act on.
func TestLoggingObserverWritesStateVersionConflictAttributes(t *testing.T) {
	t.Parallel()

	var facts StateVersionConflictFacts
	facts.Record(StateAlreadyAppliedAtApply, StateVersionConflictMissing, "series-1", 7, 0, "", false)
	facts.Record(StateAlreadyAppliedAtApply, StateVersionConflictMissing, "series-2", 7, 0, "", false)
	facts.Record(StateAlreadyAppliedAtApply, StateVersionConflictRevisionMoved, "series-3", 7, 9, "PERSISTED_NEWER", true)
	facts.Record(StateAlreadyAppliedAtApply, "", "series-4", 7, 8, "PERSISTED_EQUAL", false)

	var output bytes.Buffer
	limiter, err := NewWindowLogLimiter(WindowLogLimiterConfig{Window: time.Hour, MaxEvents: 1})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := NewBoundedLogPolicy(limiter)
	if err != nil {
		t.Fatal(err)
	}
	NewLoggingObserver(New("alarmd", &output), policy).Observe(context.Background(), Observation{
		Component: ComponentState, Stage: StageStateApplied, Result: ResultFailed, Operation: OperationNormal,
		Direction: DirectionInternal, ReasonCode: "STATE_VERSION_CONFLICT", Err: errors.New("state apply did not complete: STATE_VERSION_CONFLICT (missing: expected revision 7, stored revision 0)"),
		StateVersionConflict: &facts,
	})

	var event map[string]any
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatalf("decode conflict observation log: %v; log=%s", err, output.String())
	}
	want := map[string]any{
		"state_version_conflict_apply_missing": float64(2), "state_version_conflict_apply_revision_moved": float64(1),
		"state_version_conflict_apply_other": float64(1), "reason_code": "STATE_VERSION_CONFLICT",
	}
	for field, value := range want {
		if event[field] != value {
			t.Fatalf("event[%q] = %#v, want %#v; event=%#v", field, event[field], value, event)
		}
	}
	samples, ok := event["state_version_conflict_samples"].([]any)
	if !ok || len(samples) != 3 {
		t.Fatalf("state_version_conflict_samples = %#v, want three, one per kind; event=%#v", event["state_version_conflict_samples"], event)
	}
	first, _ := samples[0].(map[string]any)
	second, _ := samples[1].(map[string]any)
	third, _ := samples[2].(map[string]any)
	if first["kind"] != "missing" || first["series_identity_digest"] != "series-1" || first["expected_revision"] != float64(7) || first["stored_revision"] != float64(0) {
		t.Fatalf("first sample = %#v, want missing on series-1, expected 7, stored 0", first)
	}
	if _, present := first["stored_version_comparison"]; present {
		t.Fatalf("a missing key has no stored version to compare, sample = %#v", first)
	}
	if second["kind"] != "revision_moved" || second["stored_revision"] != float64(9) || second["stored_version_comparison"] != "PERSISTED_NEWER" || second["repeated_key"] != true {
		t.Fatalf("second sample = %#v, want revision_moved 7 -> 9 PERSISTED_NEWER on a repeated key", second)
	}
	if third["kind"] != "other" {
		t.Fatalf("third sample = %#v, want the unnamed kind folded to other", third)
	}
}

func TestStateVersionConflictFactsKeepOneSamplePerKindAndFoldUnnamedKinds(t *testing.T) {
	t.Parallel()

	for _, kind := range AllStateVersionConflictKinds() {
		if NormalizeStateVersionConflictKind(kind) != kind {
			t.Fatalf("named kind %s did not survive normalisation", kind)
		}
	}
	for _, kind := range []StateVersionConflictKind{"", "something_new", "MISSING"} {
		if got := NormalizeStateVersionConflictKind(kind); got != stateVersionConflictOther {
			t.Fatalf("unnamed kind %q normalised to %s, want other", kind, got)
		}
	}
	var facts StateVersionConflictFacts
	if !facts.Empty() {
		t.Fatal("zero facts are not empty")
	}
	facts.Record(StateAlreadyAppliedAtPreflight, StateVersionConflictSameVersionOtherStatement, "a", 3, 3, "PERSISTED_EQUAL", false)
	facts.Record(StateAlreadyAppliedAtApply, StateVersionConflictSameVersionOtherStatement, "b", 4, 4, "PERSISTED_EQUAL", false)
	facts.Record(StateAlreadyAppliedAtApply, StateVersionConflictRevisionReset, "c", 5, 1, "PERSISTED_OLDER", false)
	if facts.Empty() || len(facts.Counts) != 3 || len(facts.Samples) != 2 {
		t.Fatalf("facts = %+v, want three counted pairs and one sample per kind", facts)
	}
	if facts.Samples[0].SeriesIdentity != "a" || facts.Samples[0].Site != StateAlreadyAppliedAtPreflight ||
		facts.Samples[1].Kind != StateVersionConflictRevisionReset || facts.Samples[1].StoredRevision != 1 {
		t.Fatalf("samples = %+v, want the first of each kind: a at preflight, then c reset to 1", facts.Samples)
	}
}
