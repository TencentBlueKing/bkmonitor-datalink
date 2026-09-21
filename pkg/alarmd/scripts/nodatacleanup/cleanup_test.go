// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"path"
	"sort"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/state"
)

const testPrefix = "alarmd"

// fakeStore is a keyspace with the two shapes this walks, and a glob it applies
// itself. The pattern match is the thing under test in one of the cases below,
// so it is evaluated here rather than assumed: a fake that returned every key
// whatever the pattern would let a cleanup that walks live memory pass.
type fakeStore struct {
	values  map[string][]byte
	hashes  map[string]map[string][]byte
	dumps   map[string][]byte
	deleted []string
	// scanned is every key the walk was offered, so a test can say what the
	// pattern reached rather than only what survived the version check.
	scanned []string
	failDel bool
}

func (store *fakeStore) Scan(
	_ context.Context, _ uint64, match string, _ int64,
) ([]string, uint64, error) {
	keys := make([]string, 0, len(store.values))
	for key := range store.values {
		matched, err := path.Match(match, key)
		if err != nil {
			return nil, 0, err
		}
		if matched {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	store.scanned = append(store.scanned, keys...)
	return keys, 0, nil
}

func (store *fakeStore) Get(_ context.Context, key string) ([]byte, error) {
	value, found := store.values[key]
	if !found {
		return nil, nil
	}
	return value, nil
}

func (store *fakeStore) HGet(_ context.Context, key, field string) ([]byte, error) {
	value, found := store.hashes[key][field]
	if !found {
		return nil, nil
	}
	return value, nil
}

func (store *fakeStore) Dump(_ context.Context, key string) ([]byte, error) {
	if _, found := store.values[key]; !found {
		return nil, nil
	}
	if dump, found := store.dumps[key]; found {
		return dump, nil
	}
	return []byte("dump:" + key), nil
}

func (store *fakeStore) Del(_ context.Context, key string) error {
	if store.failDel {
		return errors.New("the store refused the delete")
	}
	delete(store.values, key)
	store.deleted = append(store.deleted, key)
	return nil
}

type recordingCopier struct {
	saved map[string][]byte
	fail  bool
}

func (copier *recordingCopier) Save(key string, dump []byte) error {
	if copier.fail {
		return errors.New("the copy could not be written")
	}
	if copier.saved == nil {
		copier.saved = map[string][]byte{}
	}
	copier.saved[key] = dump
	return nil
}

func planIdentity(strategy string) execution.PlanNoDataIdentity {
	return execution.PlanNoDataIdentity{
		Plan: execution.PlanIdentity{
			TenantID: "tenant", BusinessID: "2", StrategyID: strategy,
		},
		StateGeneration: "generation-1",
	}
}

func keysFor(t *testing.T, strategy string) (string, string) {
	t.Helper()
	blob, err := state.PlanNoDataKeyV2(testPrefix, planIdentity(strategy))
	if err != nil {
		t.Fatal(err)
	}
	hash, err := state.PlanNoDataHashKeyV2(testPrefix, planIdentity(strategy))
	if err != nil {
		t.Fatal(err)
	}
	return blob, hash
}

func version(rounds int64) execution.ApplyVersion {
	return execution.ApplyVersion{
		StateApplyEpoch: 1, EvaluationTime: execution.EvaluationTime(1700000000 + rounds*60), SlotDigest: "slot",
	}
}

func wholeMemory(t *testing.T, applied execution.ApplyVersion) []byte {
	t.Helper()
	encoded, err := json.Marshal(map[string]any{
		"schema": "alarmd-plan-no-data", "version": 1, "apply_version": applied,
		"marker_revision": 4, "roster_version": "TARGET_STATIC/1",
	})
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func perGroupHeaderBytes(t *testing.T, applied execution.ApplyVersion) []byte {
	t.Helper()
	encoded, err := json.Marshal(map[string]any{
		"schema": "alarmd-plan-no-data", "version": 2, "apply_version": applied,
		"marker_revision": 2, "memory_digest": "digest", "present_as_of": 1700000000,
	})
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

// The walk reaches the records it replaces and no others.
//
// This is the case worth stating on its own, because everything else the
// cleanup does is downstream of which keys it was handed. The two
// representations live under one prefix and differ by one segment, and a
// pattern that reached both would put live memory one version comparison away
// from being deleted.
func TestTheWalkReachesOnlyTheRecordsItReplaces(t *testing.T) {
	blob, hash := keysFor(t, "7")
	store := &fakeStore{values: map[string][]byte{
		blob: wholeMemory(t, version(0)),
		// The live record, in the keyspace the walk runs over. Put here as a
		// value rather than a hash on purpose: the fake offers Scan every key
		// it holds, so if the pattern matched this one the walk would see it.
		hash: []byte("the memory this build writes"),
	}}
	counts, err := Run(context.Background(), store, Options{Prefix: testPrefix})
	if err != nil {
		t.Fatal(err)
	}
	if len(store.scanned) != 1 || store.scanned[0] != blob {
		t.Fatalf("the walk was offered %v, want only the whole-memory key %q", store.scanned, blob)
	}
	if counts.Enumerated != 1 {
		t.Fatalf("counts = %s, want one record enumerated", counts)
	}
}

// Without -delete nothing is written, and the counts still say what would go.
func TestEnumeratingWritesNothing(t *testing.T) {
	blob, hash := keysFor(t, "7")
	store := &fakeStore{
		values: map[string][]byte{blob: wholeMemory(t, version(0))},
		hashes: map[string]map[string][]byte{hash: {"_meta": perGroupHeaderBytes(t, version(1))}},
	}
	counts, err := Run(context.Background(), store, Options{Prefix: testPrefix})
	if err != nil {
		t.Fatal(err)
	}
	want := Counts{Enumerated: 1, WithPerGroupRecord: 1, Eligible: 1}
	if counts != want {
		t.Fatalf("counts = %s, want %s", counts, want.String())
	}
	if len(store.deleted) != 0 || store.values[blob] == nil {
		t.Fatal("a run without -delete removed a record")
	}
}

// The four numbers are four different situations, and the gaps between them are
// the reading. A run whose eligible count is short of its enumerated one is not
// a run that went wrong.
func TestTheCountsSeparateTheReasonsARecordStays(t *testing.T) {
	eligible, eligibleHash := keysFor(t, "1")
	noHash, _ := keysFor(t, "2")
	behind, behindHash := keysFor(t, "3")
	unreadable, unreadableHash := keysFor(t, "4")

	store := &fakeStore{
		values: map[string][]byte{
			eligible: wholeMemory(t, version(0)),
			// Never run since the upgrade: its only memory is this record.
			noHash: wholeMemory(t, version(0)),
			// Went back to a build that writes the old record, so the old one
			// is the newer one and deleting it would lose those rounds.
			behind: wholeMemory(t, version(5)),
			// Bytes nobody can read. The version check cannot be run on it, and
			// it is the record most worth keeping.
			unreadable: []byte("{"),
		},
		hashes: map[string]map[string][]byte{
			eligibleHash:   {"_meta": perGroupHeaderBytes(t, version(1))},
			behindHash:     {"_meta": perGroupHeaderBytes(t, version(2))},
			unreadableHash: {"_meta": perGroupHeaderBytes(t, version(9))},
		},
	}
	copier := &recordingCopier{}
	counts, err := Run(context.Background(), store, Options{Prefix: testPrefix, Delete: true, Copier: copier})
	if err != nil {
		t.Fatal(err)
	}
	want := Counts{Enumerated: 4, WithPerGroupRecord: 2, Eligible: 1, Deleted: 1, Unreadable: 1}
	if counts != want {
		t.Fatalf("counts = %s, want %s", counts, want.String())
	}
	if len(store.deleted) != 1 || store.deleted[0] != eligible {
		t.Fatalf("deleted %v, want only the record whose replacement is at least as new", store.deleted)
	}
	for name, key := range map[string]string{"no per-group record": noHash, "behind": behind, "unreadable": unreadable} {
		if store.values[key] == nil {
			t.Fatalf("the %s record was deleted", name)
		}
	}
}

// Existence of the per-group record is not the predicate. A Plan that moved
// back to an older build has a newer old record, and that is the one case where
// deleting loses rounds nobody can get back.
func TestARecordNewerThanItsReplacementIsKept(t *testing.T) {
	blob, hash := keysFor(t, "7")
	store := &fakeStore{
		values: map[string][]byte{blob: wholeMemory(t, version(9))},
		hashes: map[string]map[string][]byte{hash: {"_meta": perGroupHeaderBytes(t, version(1))}},
	}
	copier := &recordingCopier{}
	counts, err := Run(context.Background(), store, Options{Prefix: testPrefix, Delete: true, Copier: copier})
	if err != nil {
		t.Fatal(err)
	}
	if counts.WithPerGroupRecord != 1 {
		t.Fatalf("counts = %s, want the per-group record found", counts)
	}
	if counts.Eligible != 0 || counts.Deleted != 0 {
		t.Fatalf("counts = %s, want the newer record kept", counts)
	}
	// Equal versions do qualify: one Slot writes one representation, so two
	// records at one version means the new one already states that round.
	store.hashes[hash]["_meta"] = perGroupHeaderBytes(t, version(9))
	counts, err = Run(context.Background(), store, Options{Prefix: testPrefix, Delete: true, Copier: copier})
	if err != nil {
		t.Fatal(err)
	}
	if counts.Deleted != 1 {
		t.Fatalf("counts = %s, want a record whose replacement states the same round removed", counts)
	}
}

// A copy that did not land stops the run before the delete. The copy is the
// only thing that makes this reversible, so the two cannot be reordered and the
// failure cannot be swallowed.
func TestACopyThatFailedStopsTheDelete(t *testing.T) {
	blob, hash := keysFor(t, "7")
	store := &fakeStore{
		values: map[string][]byte{blob: wholeMemory(t, version(0))},
		hashes: map[string]map[string][]byte{hash: {"_meta": perGroupHeaderBytes(t, version(1))}},
	}
	_, err := Run(context.Background(), store, Options{
		Prefix: testPrefix, Delete: true, Copier: &recordingCopier{fail: true},
	})
	if err == nil {
		t.Fatal("a run whose copy could not be written carried on")
	}
	if len(store.deleted) != 0 || store.values[blob] == nil {
		t.Fatal("a record was deleted after its copy failed to write")
	}
}

// The copy holds what RESTORE takes back, for the keys that were removed and
// for no others.
func TestEveryDeletedRecordIsSavedFirst(t *testing.T) {
	blob, hash := keysFor(t, "7")
	other, _ := keysFor(t, "8")
	store := &fakeStore{
		values: map[string][]byte{
			blob:  wholeMemory(t, version(0)),
			other: wholeMemory(t, version(0)),
		},
		hashes: map[string]map[string][]byte{hash: {"_meta": perGroupHeaderBytes(t, version(1))}},
		dumps:  map[string][]byte{blob: []byte("serialised")},
	}
	copier := &recordingCopier{}
	if _, err := Run(context.Background(), store, Options{
		Prefix: testPrefix, Delete: true, Copier: copier,
	}); err != nil {
		t.Fatal(err)
	}
	if len(copier.saved) != 1 || string(copier.saved[blob]) != "serialised" {
		t.Fatalf("saved = %v, want the serialisation of the one deleted record", copier.saved)
	}
}

// Deleting without anywhere to put the copies is refused before the walk
// starts, rather than at the first key that qualifies.
func TestDeletingRequiresSomewhereToSaveTheCopies(t *testing.T) {
	if _, err := Run(context.Background(), &fakeStore{}, Options{Prefix: testPrefix, Delete: true}); err == nil {
		t.Fatal("a delete run with no copier was allowed to start")
	}
}
