// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package state

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A series is read from the framed key and written there, from each shape it
// can be in: no record yet, or a framed record. The load and the apply are
// exercised together, because a compare-and-set that expects a revision the
// framed key never had is invisible to either alone: the load reports a good
// view and the apply reports a good conflict.
func TestARecordIsReadFromTheFramedKeyAndWrittenThere(t *testing.T) {
	version := applyVersion()
	cases := []struct {
		name           string
		framed         *execution.ApplyVersion
		framedRev      uint64
		wantView       execution.StateRepresentation
		wantViewRev    uint64
		wantWrittenRev uint64
	}{
		{name: "first write: no record yet", wantView: "", wantViewRev: 0, wantWrittenRev: 1},
		{name: "steady: the framed record exists", framed: &version, framedRev: 3,
			wantView: execution.StateRepresentationFramed, wantViewRev: 3, wantWrittenRev: 4},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			backend := newPipelineMemoryBackend()
			store := newBatchStore(t, backend, nil)
			identity := seriesIdentity(0)
			framedKey, _ := RuntimeStateKeyV3("alarmd", identity)
			if test.framed != nil {
				backend.values[framedKey], _ = encodeRuntimePacked(seriesMutation(t, identity, *test.framed, 0, ""), test.framedRev)
			}
			next := execution.ApplyVersion{StateApplyEpoch: 1, EvaluationTime: 120, SlotDigest: "slot-2"}
			loaded, err := store.LoadRuntime(context.Background(), execution.StatePreflightRequest{Contract: frozenRef(),
				Items: []execution.StatePreflightItem{{Identity: identity, ApplyVersion: next}}})
			if err != nil {
				t.Fatal(err)
			}
			view := loaded.Items[0]
			if view.Representation != test.wantView || view.BlobRevision != test.wantViewRev {
				t.Fatalf("loaded view = %q at revision %d, want %q at revision %d",
					view.Representation, view.BlobRevision, test.wantView, test.wantViewRev)
			}
			mutation := seriesMutation(t, identity, next, view.BlobRevision, "")
			applied, err := store.ApplyRuntime(context.Background(), execution.StateApplyRequest{Contract: frozenRef(),
				Retention: testRetention(), Items: []execution.StateMutation{mutation}})
			if err != nil {
				t.Fatal(err)
			}
			if applied.Items[0].Status != execution.StateApplied {
				t.Fatalf("apply = %+v, want APPLIED", applied.Items[0])
			}
			written := decodeRuntime(backend.values[framedKey], identity, frozenRef(), next)
			if written.Representation != execution.StateRepresentationFramed || written.BlobRevision != test.wantWrittenRev ||
				written.PersistedApplyVersion != next {
				t.Fatalf("framed key after apply = %s revision %d version %+v, want framed revision %d at the applied version",
					written.Representation, written.BlobRevision, written.PersistedApplyVersion, test.wantWrittenRev)
			}
		})
	}
}

// A record under the runtime key that predates the framed one is not read.
// No binary since the framed record writes that key, the previous upstream
// snapshot included, so the series starts warming as a new one does, the
// write creates the framed record, and the older key is left to its TTL.
func TestARecordUnderTheOlderRuntimeKeyIsNotRead(t *testing.T) {
	backend := newPipelineMemoryBackend()
	store := newBatchStore(t, backend, nil)
	identity := seriesIdentity(0)
	framedKey, _ := RuntimeStateKeyV3("alarmd", identity)
	series, err := contract.DeriveCanonicalDigestV2("alarmd-runtime-series-v2", identity.SeriesIdentityDigest)
	if err != nil {
		t.Fatal(err)
	}
	olderKey := executionKey("alarmd", "runtime", identity.Plan, identity.StateGeneration, series)
	older := []byte(`{"schema":"alarmd-runtime-state-v2","blob_revision":9}`)
	backend.values[olderKey] = older

	next := execution.ApplyVersion{StateApplyEpoch: 1, EvaluationTime: 120, SlotDigest: "slot-2"}
	loaded, err := store.LoadRuntime(context.Background(), execution.StatePreflightRequest{Contract: frozenRef(),
		Items: []execution.StatePreflightItem{{Identity: identity, ApplyVersion: next}}})
	if err != nil {
		t.Fatal(err)
	}
	if view := loaded.Items[0]; view.Status != execution.StateMissingWarming || view.BlobRevision != 0 {
		t.Fatalf("loaded view = %s at revision %d, want MISSING_WARMING: the older key is not read", view.Status, view.BlobRevision)
	}
	applied, err := store.ApplyRuntime(context.Background(), execution.StateApplyRequest{Contract: frozenRef(),
		Retention: testRetention(), Items: []execution.StateMutation{seriesMutation(t, identity, next, 0, "")}})
	if err != nil {
		t.Fatal(err)
	}
	if applied.Items[0].Status != execution.StateApplied || backend.values[framedKey] == nil {
		t.Fatalf("apply = %+v, want APPLIED onto the framed key", applied.Items[0])
	}
	if string(backend.values[olderKey]) != string(older) {
		t.Fatal("the older runtime key was written; nothing writes it")
	}
}

// A conflict met on the framed key is named in the framed key's own revision
// space. The write was built from a missing record and expected the framed
// key to be missing; another writer created it at revision 1 with a different
// statement. That is a move on the framed key (0 -> 1).
func TestAConflictOnTheFramedKeyIsNamedInItsOwnRevisionSpace(t *testing.T) {
	backend := newPipelineMemoryBackend()
	store := newBatchStore(t, backend, fixedFenceKeys{testFenceKeys()})
	identity := seriesIdentity(0)
	framedKey, _ := RuntimeStateKeyV3("alarmd", identity)
	next := execution.ApplyVersion{StateApplyEpoch: 1, EvaluationTime: 120, SlotDigest: "slot-2"}

	mutation := seriesMutation(t, identity, next, 0, "")
	if _, err := store.LoadRuntime(context.Background(), execution.StatePreflightRequest{Contract: frozenRef(),
		Items: preflightItems([]execution.StateMutation{mutation})}); err != nil {
		t.Fatal(err)
	}
	// Another writer lands the framed key first, at the same version with a
	// different statement.
	backend.values[framedKey], _ = encodeRuntimePacked(seriesMutation(t, identity, next, 0, "theirs"), 1)

	result, err := store.ApplyRuntimeFenced(context.Background(), execution.StateApplyRequest{Contract: frozenRef(),
		Retention: testRetention(), Items: []execution.StateMutation{mutation}}, testApplyFence())
	if err != nil {
		t.Fatal(err)
	}
	item := result.Items[0]
	if item.Status != execution.StateApplyVersionConflict || item.VersionConflict != execution.StateVersionConflictRevisionMoved {
		t.Fatalf("item = %+v, want VERSION_CONFLICT named revision_moved: the framed key went from missing to revision 1", item)
	}
}

// A frozen series is renewed under the framed key, and an item that does not
// say its record is framed is refused by name rather than renewed under a
// guess.
func TestAFrozenSeriesIsRenewedUnderTheKeyItsRecordLivesIn(t *testing.T) {
	backend := &casMemoryBackend{values: make(map[string][]byte)}
	router, err := NewFixedRouter("target", backend)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewExecutionStore(ExecutionStoreOptions{Prefix: "alarmd", Router: router, MaxValueBytes: 4096,
		MaxItemsPerCall: 4, MinTTL: time.Minute, MaxTTL: time.Hour, RestartMargin: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	identity := stateIdentityV2()
	framedKey, _ := RuntimeStateKeyV3("alarmd", identity)
	ttl, err := store.runtimeTTL(testRetention(), 0)
	if err != nil {
		t.Fatal(err)
	}
	runningOut := writtenAt().Add(ttl - time.Second)
	for _, test := range []struct {
		name           string
		representation execution.StateRepresentation
		wantKey        string
		wantOutcome    execution.FrozenRenewalOutcome
	}{
		{name: "framed", representation: execution.StateRepresentationFramed, wantKey: framedKey, wantOutcome: execution.FrozenRenewalRenewed},
		{name: "unsaid", representation: "", wantKey: "", wantOutcome: execution.FrozenRenewalFailed},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend.renewals = nil
			backend.values[framedKey], _ = encodeRuntimePacked(seriesMutation(t, identity, applyVersion(), 0, ""), 1)
			backend.writeTTLs = map[string]time.Duration{framedKey: ttl}
			result, err := store.RenewFrozenRuntime(context.Background(), execution.FrozenStateRenewalRequest{
				Contract: frozenRef(), Retention: testRetention(), Now: runningOut,
				Items: []execution.FrozenSeriesState{{Identity: identity, LastApplied: applyVersion().EvaluationTime,
					Representation: test.representation}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if result.Items[0].Outcome != test.wantOutcome {
				t.Fatalf("outcome = %s, want %s", result.Items[0].Outcome, test.wantOutcome)
			}
			if test.wantKey == "" {
				if len(backend.renewals) != 0 {
					t.Fatalf("an item that does not say which key holds its record renewed %+v; a guess that lands on "+
						"the other key reports renewed while the record runs out", backend.renewals)
				}
				return
			}
			if len(backend.renewals) != 1 || backend.renewals[0].Key != test.wantKey {
				t.Fatalf("renewals = %+v, want exactly the %s key %q", backend.renewals, test.name, test.wantKey)
			}
		})
	}
}

// A frame this binary does not know is a newer binary's record. It is refused
// by name, not written over - a whole write over it would roll the series back
// silently on every cross-frame-version rollback.
func TestANewerFrameIsRefusedByNameNotWrittenOver(t *testing.T) {
	backend := newPipelineMemoryBackend()
	store := newBatchStore(t, backend, nil)
	identity := seriesIdentity(0)
	framedKey, _ := RuntimeStateKeyV3("alarmd", identity)
	framed, _ := encodeRuntimePacked(seriesMutation(t, identity, applyVersion(), 0, ""), 2)
	// One past the newest schema this build reads, which has to be kept one
	// past it: written as an offset from an older constant this silently
	// became a schema the build DOES read the moment a newer one was added,
	// and the case went on passing while testing nothing.
	framed[4] = packedFrameSchemaV2 + 1
	backend.values[framedKey] = framed
	framedBefore := string(framed)

	next := execution.ApplyVersion{StateApplyEpoch: 1, EvaluationTime: 120, SlotDigest: "slot-2"}
	loaded, err := store.LoadRuntime(context.Background(), execution.StatePreflightRequest{Contract: frozenRef(),
		Items: []execution.StatePreflightItem{{Identity: identity, ApplyVersion: next}}})
	if err != nil {
		t.Fatal(err)
	}
	view := loaded.Items[0]
	if view.Status != execution.StateDeterministicInvalid || view.ReasonCode != execution.ReasonCode(contract.ReasonStateSchemaUnsupported) {
		t.Fatalf("loaded view = %s (%s), want DETERMINISTIC_INVALID STATE_SCHEMA_UNSUPPORTED: a frame a newer "+
			"binary wrote is not this binary's to read", view.Status, view.ReasonCode)
	}
	applied, err := store.ApplyRuntime(context.Background(), execution.StateApplyRequest{Contract: frozenRef(),
		Retention: testRetention(), Items: []execution.StateMutation{seriesMutation(t, identity, next, 5, "")}})
	if err != nil {
		t.Fatal(err)
	}
	if applied.Items[0].Status != execution.StateApplyDeterministicInvalid || string(backend.values[framedKey]) != framedBefore {
		t.Fatalf("apply = %+v, framed changed = %v; a newer binary's frame must not be written over", applied.Items[0],
			string(backend.values[framedKey]) != framedBefore)
	}
}
