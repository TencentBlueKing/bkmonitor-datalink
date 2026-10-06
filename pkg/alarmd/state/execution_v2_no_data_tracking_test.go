// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package state

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// The stored form has two encoders that list their fields rather than copying
// a struct - the delta builder above this layer and the hash encoder in it -
// so a field can reach one and not the other. This drives the bytes: what the
// encoder wrote is what the decoder is asked to read back.
func TestTheStoredFormCarriesTheTrackingFactsBothWays(t *testing.T) {
	identity := execution.PlanNoDataIdentity{
		Plan:            execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "7"},
		StateGeneration: "generation-1",
	}
	mutation := execution.PlanNoDataMutation{
		Set: []execution.NoDataGroupDelta{{
			GroupKey: "a",
			Absent:   &execution.NoDataGroupAbsence{LastSeen: 80, FirstAbsent: 85, SuppressedAt: 90},
		}},
	}
	set, _, err := encodeNoDataDelta(mutation)
	if err != nil {
		t.Fatal(err)
	}
	if len(set) != 1 {
		t.Fatalf("encoded %d fields, want one", len(set))
	}
	group, ok := decodeNoDataGroupValue("a", set[0].Value, 90)
	if !ok {
		t.Fatalf("the encoder's own bytes did not decode: %s", set[0].Value)
	}
	if group.SuppressedAt != 90 {
		t.Fatalf("stored group decodes with suppressed-at %d, want 90; the encoder dropped it "+
			"and the group reads back as still tracked", group.SuppressedAt)
	}
	if group.LastSeen != 80 || group.FirstAbsent != 85 {
		t.Fatalf("suppression displaced the timestamps beside it: %+v", group)
	}

	// The Plan-level fact travels in the header, which is the field a write
	// compares against. A header that dropped it would let the next round read
	// the roster as never exhausted and rebuild the whole-item absence.
	header, err := json.Marshal(noDataHashHeader{
		Schema: executionNoDataSchema, Version: execution.WrittenNoDataMemorySchema, Identity: identity,
		MarkerRevision: 1, PresentAsOf: 90, TrackingExhaustedAt: 90,
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, decoded, ok := decodeNoDataHashHeader(header, identity)
	if !ok {
		t.Fatalf("the header this build writes did not decode: %+v", snapshot)
	}
	if decoded.TrackingExhaustedAt != 90 || snapshot.TrackingExhaustedAt != 90 {
		t.Fatalf("header decodes with tracking-exhausted %d and reports %d, want 90 for both",
			decoded.TrackingExhaustedAt, snapshot.TrackingExhaustedAt)
	}
}

// The two facts have to survive the path production uses, which is not the
// path the encoder test above drives. That one calls the encode and decode
// helpers directly, so it says the helpers agree with each other and nothing
// about whether the store's own header write carries the Plan-level fact -
// and the store builds its header by listing fields, like everything else on
// this path. Deleting that one field left every package green until this
// existed.
func TestTheTrackingFactsSurviveTheStore(t *testing.T) {
	backend := &casMemoryBackend{values: make(map[string][]byte)}
	store := generationStore(t, backend)
	ctx := context.Background()

	suppressed := execution.NoDataGroupMemory{GroupKey: "a", FirstAbsent: 940, SuppressedAt: 960}
	first := noDataMutationFrom(t, execution.PlanNoDataMemoryUpdate{
		DerivedFrom: execution.NoDataRepresentationNone,
		Identity:    noDataIdentityV2(), ApplyVersion: applyVersion(),
		ScheduleRevision: "plan-r1", RosterVersion: "TARGET_STATIC/1",
		PresentAsOf: noDataPresentAsOf, Memory: []execution.NoDataGroupMemory{suppressed},
	})
	applied, err := store.ApplyNoData(ctx, execution.NoDataApplyRequest{
		Contract: frozenRef(), Items: []execution.PlanNoDataMutation{first},
	})
	if err != nil {
		t.Fatal(err)
	}
	if applied.Items[0].Status != execution.NoDataApplied {
		t.Fatalf("apply = %+v, want it applied", applied.Items[0])
	}
	loaded, err := store.LoadNoData(ctx, execution.NoDataLoadRequest{
		Contract: frozenRef(), Items: []execution.PlanNoDataLoadItem{noDataLoadItemV2()},
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := loaded.Items[0]
	if len(snapshot.Groups) != 1 || snapshot.Groups[0].SuppressedAt != 960 {
		t.Fatalf("a suppressed group came back as %+v; through the store it reads as still tracked",
			snapshot.Groups)
	}
	if snapshot.TrackingExhaustedAt != 0 {
		t.Fatalf("a Plan nobody exhausted came back exhausted at %d", snapshot.TrackingExhaustedAt)
	}

	// The last tracked group goes, and the Plan-level fact takes its place.
	// This is the shape the horizon leaves behind, and the one the next round
	// reads to tell an exhausted roster from a Plan that never had groups.
	second := noDataMutationFrom(t, execution.PlanNoDataMemoryUpdate{
		DerivedFrom: execution.NoDataRepresentationPerGroup, LoadedApplyVersion: applyVersion(),
		ExpectedMarkerRevision: snapshot.MarkerRevision,
		Identity:               noDataIdentityV2(), ApplyVersion: execution.ApplyVersion{StateApplyEpoch: 1, EvaluationTime: 120, SlotDigest: "slot"},
		ScheduleRevision: "plan-r1", RosterVersion: "TARGET_STATIC/1",
		PresentAsOf: noDataPresentAsOf, TrackingExhaustedAt: 970,
		Loaded: []execution.NoDataGroupMemory{suppressed}, LoadedPresentAsOf: noDataPresentAsOf,
	})
	applied, err = store.ApplyNoData(ctx, execution.NoDataApplyRequest{
		Contract: frozenRef(), Items: []execution.PlanNoDataMutation{second},
	})
	if err != nil {
		t.Fatal(err)
	}
	if applied.Items[0].Status != execution.NoDataApplied {
		t.Fatalf("apply = %+v, want it applied", applied.Items[0])
	}
	loaded, err = store.LoadNoData(ctx, execution.NoDataLoadRequest{
		Contract: frozenRef(), Items: []execution.PlanNoDataLoadItem{noDataLoadItemV2()},
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot = loaded.Items[0]
	if snapshot.TrackingExhaustedAt != 970 {
		t.Fatalf("the exhausted roster came back with tracking-exhausted %d, want 970; "+
			"an empty roster that reads as never exhausted becomes a whole-item absence next round",
			snapshot.TrackingExhaustedAt)
	}
	if len(snapshot.Groups) != 0 {
		t.Fatalf("the exhausted roster still holds %+v", snapshot.Groups)
	}
}

// A record written before these fields existed decodes with both at zero,
// which reads as still tracking and not exhausted. Nothing migrates it; the
// first round under this build decides, from the horizon, what it should be.
func TestARecordWrittenBeforeTheTrackingFactsDecodesAsStillTracking(t *testing.T) {
	identity := execution.PlanNoDataIdentity{
		Plan:            execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "7"},
		StateGeneration: "generation-1",
	}
	// Exactly the bytes a v2 build wrote: no suppressed_at, no
	// tracking_exhausted_at, and a version this build still reads.
	group, ok := decodeNoDataGroupValue("a", []byte(`{"last_seen":80,"first_absent":85}`), 90)
	if !ok {
		t.Fatal("a record written by the previous build did not decode")
	}
	if group.SuppressedAt != 0 {
		t.Fatalf("an old group decodes as suppressed at %d", group.SuppressedAt)
	}
	// A suppression that is not a time is refused with the timestamps beside
	// it, rather than read as some round before the epoch.
	if _, ok := decodeNoDataGroupValue("a", []byte(`{"first_absent":85,"suppressed_at":-1}`), 90); ok {
		t.Fatal("a negative suppression decoded")
	}
	// Built by leaving the new fields unset rather than by hand-writing the
	// JSON: omitempty then produces exactly the bytes a v2 build wrote, and the
	// identity is encoded the one way this package encodes it.
	header, err := json.Marshal(noDataHashHeader{
		Schema: executionNoDataSchema, Version: execution.NoDataMemorySchemaV2, Identity: identity,
		MarkerRevision: 1, PresentAsOf: 90,
	})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(header, []byte("tracking_exhausted_at")) {
		t.Fatalf("a header with no tracking fact still wrote the key: %s", header)
	}
	snapshot, decoded, ok := decodeNoDataHashHeader(header, identity)
	if !ok {
		t.Fatalf("a v2 header did not decode: %+v", snapshot)
	}
	if decoded.TrackingExhaustedAt != 0 || snapshot.TrackingExhaustedAt != 0 {
		t.Fatal("a record written before the horizon existed reads as having exhausted its roster")
	}
	if snapshot.SchemaVersion != execution.NoDataMemorySchemaV2 {
		t.Fatalf("v2 header reports schema %d", snapshot.SchemaVersion)
	}
}
