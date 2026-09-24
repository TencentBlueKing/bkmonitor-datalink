// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package state

import (
	"context"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A series with no record under its generation, whose Plan carries history
// from the generation it moved from, is read there too: what it held rides
// beside the missing view, counted, with its bytes in the round's total. A
// series with a record of its own, or a Plan that carries nothing, is not
// read again.
func TestTheCarryPassReadsThePreviousGenerationBesideTheMissingRecord(t *testing.T) {
	version := applyVersion()
	backend := newPipelineMemoryBackend()
	store := newBatchStore(t, backend, nil)
	const previous = execution.StateGeneration("previous-generation")
	const (
		carried = iota // no record now; the previous generation holds one
		missing        // no record now or before
		rotten         // no record now; the previous frame does not read
		own            // its own record: not read again
		plain          // no record, and its Plan carries nothing
		shapes
	)
	items := make([]execution.StatePreflightItem, shapes)
	var carriedBytes int
	for shape := range items {
		identity := seriesIdentity(shape)
		items[shape] = execution.StatePreflightItem{Identity: identity, ApplyVersion: version, CarryFrom: previous}
		old := identity
		old.StateGeneration = previous
		oldKey, _ := RuntimeStateKeyV3("alarmd", old)
		switch shape {
		case carried:
			backend.values[oldKey], _ = encodeRuntimePacked(seriesMutation(t, old, version, 0, ""), 4)
			carriedBytes = len(backend.values[oldKey])
		case rotten:
			backend.values[oldKey] = []byte("not-a-frame")
		case own:
			key, _ := RuntimeStateKeyV3("alarmd", identity)
			backend.values[key], _ = encodeRuntimePacked(seriesMutation(t, identity, version, 0, ""), 3)
			backend.values[oldKey], _ = encodeRuntimePacked(seriesMutation(t, old, version, 0, ""), 4)
		case plain:
			items[shape].CarryFrom = ""
			backend.values[oldKey], _ = encodeRuntimePacked(seriesMutation(t, old, version, 0, ""), 4)
		}
	}
	withoutCarry := make([]execution.StatePreflightItem, shapes)
	copy(withoutCarry, items)
	for index := range withoutCarry {
		withoutCarry[index].CarryFrom = ""
	}
	baseline, err := store.LoadRuntime(context.Background(), execution.StatePreflightRequest{Contract: frozenRef(), Items: withoutCarry})
	if err != nil {
		t.Fatal(err)
	}

	loaded, err := store.LoadRuntime(context.Background(), execution.StatePreflightRequest{Contract: frozenRef(), Items: items})
	if err != nil {
		t.Fatal(err)
	}
	view := loaded.Items[carried]
	if view.Status != execution.StateMissingWarming || view.BlobRevision != 0 || len(view.History) != 0 {
		t.Fatalf("carried series view=%+v, want the missing record it is", view)
	}
	if view.Carried == nil || view.Carried.Status != execution.StateFoundReady || view.Carried.Identity.StateGeneration != previous || len(view.Carried.History) == 0 {
		t.Fatalf("carried=%+v, want the previous generation's record beside the view", view.Carried)
	}
	for _, shape := range []int{missing, rotten, own, plain} {
		if loaded.Items[shape].Carried != nil {
			t.Errorf("shape %d carried %+v, want nothing", shape, loaded.Items[shape].Carried)
		}
	}
	if loaded.Items[own].Status != execution.StateFoundReady {
		t.Errorf("a series with its own record read as %s", loaded.Items[own].Status)
	}
	if loaded.CarryFound != 1 || loaded.CarryMissing != 1 || loaded.CarryUnreadable != 1 {
		t.Fatalf("carry counts found=%d missing=%d unreadable=%d, want one each", loaded.CarryFound, loaded.CarryMissing, loaded.CarryUnreadable)
	}
	if got := loaded.LoadedBytes - baseline.LoadedBytes; got != int64(carriedBytes+len("not-a-frame")) {
		t.Fatalf("the carry pass added %d bytes to the round, want the %d it read", got, carriedBytes+len("not-a-frame"))
	}
}

// The carry pass reads records the frame pass could say nothing about, since
// it found none, and those records are written this round under the new
// generation at the size they were read. So neither the carry pass nor the
// next round's frame pass may batch them by what the empty frame pass
// learned: on a cold store after a release that moved every generation, that
// asked for every carried record in one read, over the byte budget, and the
// next round asked for all of them again under the new keys - a read that
// fails every time learns nothing, so it never recovered.
func TestCarriedRecordsStayWithinTheReadBudgetForTwoRounds(t *testing.T) {
	version := applyVersion()
	backend := newPipelineMemoryBackend()
	backend.recordKeyCounts = true
	store := newBatchStore(t, backend, nil)
	const previous = execution.StateGeneration("previous-generation")
	const series = 64
	padding := strings.Repeat("x", 200<<10)
	items := make([]execution.StatePreflightItem, series)
	for index := range items {
		identity := seriesIdentity(index)
		items[index] = execution.StatePreflightItem{Identity: identity, ApplyVersion: version, CarryFrom: previous}
		old := identity
		old.StateGeneration = previous
		key, _ := RuntimeStateKeyV3("alarmd", old)
		backend.values[key], _ = encodeRuntimePacked(seriesMutation(t, old, version, 0, padding), 4)
	}
	within := func(t *testing.T, round string) {
		t.Helper()
		for call, bytes := range backend.byteCounts {
			if bytes > runtimeLoadBatchBytes {
				t.Fatalf("%s: read %d asked for %d keys, %d bytes, over the %d byte budget (all reads: keys %v)",
					round, call, backend.keyCounts[call], bytes, runtimeLoadBatchBytes, backend.keyCounts)
			}
		}
		backend.keyCounts, backend.byteCounts = nil, nil
	}

	loaded, err := store.LoadRuntime(context.Background(), execution.StatePreflightRequest{Contract: frozenRef(), Items: items})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.CarryFound != series {
		t.Fatalf("carried %d of %d", loaded.CarryFound, series)
	}
	within(t, "the round that carries")

	// The round wrote each carried record under the new generation.
	for index := range items {
		key, _ := RuntimeStateKeyV3("alarmd", items[index].Identity)
		backend.values[key], _ = encodeRuntimePacked(seriesMutation(t, items[index].Identity, version, 0, padding), 1)
	}
	if _, err := store.LoadRuntime(context.Background(), execution.StatePreflightRequest{Contract: frozenRef(), Items: items}); err != nil {
		t.Fatal(err)
	}
	within(t, "the round after it")
}
