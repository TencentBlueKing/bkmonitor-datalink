// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package state

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// The migration from the envelope under runtime to the framed record under
// runtime3 has three shapes a series can be read in, and the write must land
// correctly from each of them:
//
//   - first write: only the envelope exists. The view is the envelope, the
//     write creates runtime3, and the envelope is left to its TTL.
//   - bounce: both exist and the envelope is newer, because an old binary
//     took the Query Group back during a rollout and wrote it. The frame is
//     the view and the envelope is not read: the round asks for the envelope
//     only of a series whose frame cannot answer, so a frame that reads is
//     the answer whatever the older key holds. What this gives up is named
//     in the case below; what it buys is the whole envelope out of the read
//     path of every series that has a frame.
//   - steady: runtime3 exists and is the newer (or only) one. The view is
//     the framed record.
//
// Each case is written against the load and apply paths together, because
// the defect they guard - a compare-and-set that expects the envelope's
// revision on the framed key - is invisible to either path alone: the load
// reports a perfectly good view and the apply reports a perfectly good
// conflict.
func TestARecordIsReadFromEitherKeyAndWrittenToTheFramedOne(t *testing.T) {
	version := applyVersion()
	older := execution.ApplyVersion{StateApplyEpoch: 1, EvaluationTime: 30, SlotDigest: "slot-0"}
	type seed struct {
		envelope  *execution.ApplyVersion
		envRev    uint64
		framed    *execution.ApplyVersion
		framedRev uint64
	}
	cases := []struct {
		name           string
		seed           seed
		wantView       execution.StateRepresentation
		wantViewRev    uint64
		wantWrittenRev uint64
	}{
		{name: "first write: only the envelope exists",
			seed: seed{envelope: &older, envRev: 7}, wantView: execution.StateRepresentationEnvelope, wantViewRev: 7, wantWrittenRev: 1},
		// The frame answers even though the envelope is newer. No binary of
		// this era writes the envelope, so the only way to reach this shape
		// is to run one that predates the framed record and then roll
		// forward again: that window's points are lost from the frame's
		// history and come back as the window slides, which is the price of
		// not reading 344 KB per series per round for a record with no
		// writer. A frame that cannot be read still falls back - that case
		// is below.
		{name: "bounce: the envelope is newer than the framed record, and the frame still answers",
			seed:     seed{envelope: &version, envRev: 9, framed: &older, framedRev: 3},
			wantView: execution.StateRepresentationFramed, wantViewRev: 3, wantWrittenRev: 4},
		{name: "steady: the framed record is newer than the envelope",
			seed:     seed{envelope: &older, envRev: 9, framed: &version, framedRev: 3},
			wantView: execution.StateRepresentationFramed, wantViewRev: 3, wantWrittenRev: 4},
		{name: "steady: only the framed record exists",
			seed: seed{framed: &version, framedRev: 3}, wantView: execution.StateRepresentationFramed, wantViewRev: 3, wantWrittenRev: 4},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			backend := newPipelineMemoryBackend()
			store := newBatchStore(t, backend, nil)
			identity := seriesIdentity(0)
			envelopeKey, _ := RuntimeStateKeyV2("alarmd", identity)
			framedKey, _ := RuntimeStateKeyV3("alarmd", identity)
			if test.seed.envelope != nil {
				backend.values[envelopeKey], _ = encodeRuntime(seriesMutation(t, identity, *test.seed.envelope, 0, "env"), test.seed.envRev)
			}
			if test.seed.framed != nil {
				backend.values[framedKey], _ = encodeRuntimePacked(seriesMutation(t, identity, *test.seed.framed, 0, ""), test.seed.framedRev)
			}
			envelopeBefore := string(backend.values[envelopeKey])

			next := execution.ApplyVersion{StateApplyEpoch: 1, EvaluationTime: 120, SlotDigest: "slot-2"}
			loaded, err := store.LoadRuntime(context.Background(), execution.StatePreflightRequest{Contract: frozenRef(),
				Items: []execution.StatePreflightItem{{Identity: identity, ApplyVersion: next}}})
			if err != nil {
				t.Fatal(err)
			}
			view := loaded.Items[0]
			if view.Representation != test.wantView || view.BlobRevision != test.wantViewRev {
				t.Fatalf("loaded view = %s at revision %d, want the %s at revision %d: the newer of the two records is the one read",
					view.Representation, view.BlobRevision, test.wantView, test.wantViewRev)
			}

			// The evaluator writes from the view it read: its expected revision
			// is the view's, whichever key that came from.
			mutation := seriesMutation(t, identity, next, view.BlobRevision, "")
			applied, err := store.ApplyRuntime(context.Background(), execution.StateApplyRequest{Contract: frozenRef(),
				Retention: testRetention(), Items: []execution.StateMutation{mutation}})
			if err != nil {
				t.Fatal(err)
			}
			if applied.Items[0].Status != execution.StateApplied {
				t.Fatalf("apply = %+v, want APPLIED: a write built from a view read off either key must land on the "+
					"framed key without meeting a conflict it manufactured itself", applied.Items[0])
			}
			written := decodeRuntime(backend.values[framedKey], identity, frozenRef(), next)
			if written.Representation != execution.StateRepresentationFramed || written.BlobRevision != test.wantWrittenRev ||
				written.PersistedApplyVersion != next {
				t.Fatalf("framed key after apply = %s revision %d version %+v, want framed revision %d at the applied version: "+
					"the revision continues the framed key's own count", written.Representation, written.BlobRevision,
					written.PersistedApplyVersion, test.wantWrittenRev)
			}
			if string(backend.values[envelopeKey]) != envelopeBefore {
				t.Fatal("the envelope was written; it is read only and expires on its own")
			}
		})
	}
}

// A frozen series is renewed under the key its record lives in, and an item
// that does not say which is refused by name rather than renewed under a
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
	envelopeKey, _ := RuntimeStateKeyV2("alarmd", identity)
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
		{name: "envelope", representation: execution.StateRepresentationEnvelope, wantKey: envelopeKey, wantOutcome: execution.FrozenRenewalRenewed},
		{name: "framed", representation: execution.StateRepresentationFramed, wantKey: framedKey, wantOutcome: execution.FrozenRenewalRenewed},
		{name: "unsaid", representation: "", wantKey: "", wantOutcome: execution.FrozenRenewalFailed},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend.renewals = nil
			backend.values[envelopeKey], _ = encodeRuntime(seriesMutation(t, identity, applyVersion(), 0, ""), 1)
			backend.values[framedKey], _ = encodeRuntimePacked(seriesMutation(t, identity, applyVersion(), 0, ""), 1)
			backend.writeTTLs = map[string]time.Duration{envelopeKey: ttl, framedKey: ttl}
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

// A conflict met on the framed key is named in the framed key's own revision
// space. The write was built from an envelope at revision 9 and expected the
// framed key to be missing; another writer created it at revision 1 with a
// different statement. That is a move on the framed key (0 -> 1), and
// reading it against the envelope's 9 would call it a reset (9 -> 1) - a
// kind that says a key was recreated lower, which nothing did.
func TestAConflictOnTheFramedKeyIsNamedInItsOwnRevisionSpace(t *testing.T) {
	backend := newPipelineMemoryBackend()
	store := newBatchStore(t, backend, fixedFenceKeys{testFenceKeys()})
	identity := seriesIdentity(0)
	envelopeKey, _ := RuntimeStateKeyV2("alarmd", identity)
	framedKey, _ := RuntimeStateKeyV3("alarmd", identity)
	older := execution.ApplyVersion{StateApplyEpoch: 1, EvaluationTime: 30, SlotDigest: "slot-0"}
	next := execution.ApplyVersion{StateApplyEpoch: 1, EvaluationTime: 120, SlotDigest: "slot-2"}
	backend.values[envelopeKey], _ = encodeRuntime(seriesMutation(t, identity, older, 0, "env"), 9)

	mutation := seriesMutation(t, identity, next, 9, "")
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
		t.Fatalf("item = %+v, want VERSION_CONFLICT named revision_moved: the framed key went from missing to revision 1, "+
			"and the envelope's revision 9 is not a number that key ever had", item)
	}
}

// A frame this binary does not know is a newer binary's record. It is refused
// by name, not replaced by the envelope beside it - a whole write over it
// would roll the series back silently on every cross-frame-version rollback.
func TestANewerFrameIsRefusedByNameNotReplacedByTheEnvelope(t *testing.T) {
	backend := newPipelineMemoryBackend()
	store := newBatchStore(t, backend, nil)
	identity := seriesIdentity(0)
	envelopeKey, _ := RuntimeStateKeyV2("alarmd", identity)
	framedKey, _ := RuntimeStateKeyV3("alarmd", identity)
	backend.values[envelopeKey], _ = encodeRuntime(seriesMutation(t, identity, applyVersion(), 0, ""), 5)
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
		t.Fatalf("loaded view = %s (%s), want DETERMINISTIC_INVALID STATE_SCHEMA_UNSUPPORTED: the envelope must not be "+
			"read in place of a frame a newer binary wrote", view.Status, view.ReasonCode)
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

// The envelope is read for the series whose frame cannot answer, and for no
// others. This is the whole of what the second pass costs and the whole of
// what the first one saves: a Query Group whose series all have frames reads
// one key each, and one whose series are mid-migration reads the older key
// only for those.
func TestTheEnvelopeIsReadOnlyForSeriesWhoseFrameCannotAnswer(t *testing.T) {
	version := applyVersion()
	cases := []struct {
		name      string
		seedFrame bool
		corrupt   bool
		wantReads int
	}{
		{name: "the frame answers", seedFrame: true, wantReads: 0},
		{name: "no frame at all", wantReads: 1},
		{name: "the frame does not read", seedFrame: true, corrupt: true, wantReads: 1},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			backend := newPipelineMemoryBackend()
			store := newBatchStore(t, backend, nil)
			identity := seriesIdentity(0)
			envelopeKey, _ := RuntimeStateKeyV2("alarmd", identity)
			framedKey, _ := RuntimeStateKeyV3("alarmd", identity)
			backend.values[envelopeKey], _ = encodeRuntime(seriesMutation(t, identity, version, 0, "env"), 7)
			if test.seedFrame {
				if test.corrupt {
					backend.values[framedKey] = []byte("not-json")
				} else {
					backend.values[framedKey], _ = encodeRuntimePacked(seriesMutation(t, identity, version, 0, ""), 3)
				}
			}

			loaded, err := store.LoadRuntime(context.Background(), execution.StatePreflightRequest{Contract: frozenRef(),
				Items: []execution.StatePreflightItem{{Identity: identity, ApplyVersion: version}}})
			if err != nil {
				t.Fatal(err)
			}
			if test.name == "the frame answers" && loaded.FrameCorruptRescued != 0 {
				t.Fatalf("FrameCorruptRescued = %d with no envelope read at all", loaded.FrameCorruptRescued)
			}
			if loaded.EnvelopeReads != test.wantReads {
				t.Fatalf("EnvelopeReads = %d, want %d", loaded.EnvelopeReads, test.wantReads)
			}
			// The bytes follow the reads: a round that never asked for the
			// envelope never paid for it.
			envelopeBytes := int64(len(backend.values[envelopeKey]))
			if test.wantReads == 0 && loaded.LoadedBytes >= envelopeBytes {
				t.Fatalf("LoadedBytes = %d with the envelope at %d bytes: the envelope was read for a series whose frame answered",
					loaded.LoadedBytes, envelopeBytes)
			}
			if test.wantReads == 1 && loaded.LoadedBytes < envelopeBytes {
				t.Fatalf("LoadedBytes = %d, want at least the envelope's %d: the fallback did not read it", loaded.LoadedBytes, envelopeBytes)
			}
		})
	}
}

// The envelope pass keeps every call inside the batch budget, however large
// the older records turn out to be.
//
// The shape this guards is the one the envelope read exists for: series that
// have an envelope and no frame. Every frame comes back empty, and a bound
// taken from that reads as "this Query Group's records are empty" - the item
// bound - which asks for every envelope in one call. Those are the largest
// records the store holds, so the call is an order of magnitude past the
// budget and dies on its deadline; a failed read commits no size, so the next
// round sends the same call again. The symptom this split exists to remove
// would have moved into the pass that only runs while the migration is
// unfinished.
func TestTheEnvelopePassKeepsEveryCallInsideTheBatchBudget(t *testing.T) {
	backend := newPipelineMemoryBackend()
	store := newBatchStore(t, backend, nil)
	const series = 60
	// A mixed population, which is what a Query Group changing representation
	// holds and what a Query Group whose series differ in age holds. The short
	// windows come first: a bound learned from them would lift the next batch
	// to the item cap, and the next batch is the long ones. Equal-sized
	// records are the one population that cannot show this.
	short := strings.Repeat("s", 4<<10)
	long := strings.Repeat("p", 340<<10)
	mutations := make([]execution.StateMutation, series)
	for index := range mutations {
		padding := long
		if index < 16 {
			padding = short
		}
		mutations[index] = seriesMutation(t, seriesIdentity(index), applyVersion(), 0, padding)
		key, _ := RuntimeStateKeyV2("alarmd", mutations[index].Identity)
		backend.values[key], _ = encodeRuntime(mutations[index], 1)
	}
	backend.recordKeyCounts = true

	loaded, err := store.LoadRuntime(context.Background(), execution.StatePreflightRequest{Contract: frozenRef(),
		Items: preflightItems(mutations)})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.EnvelopeReads != series {
		t.Fatalf("EnvelopeReads = %d, want %d: every series here is answered by the envelope", loaded.EnvelopeReads, series)
	}
	for index, bytes := range backend.byteCounts {
		if bytes > int(runtimeLoadBatchBytes) {
			t.Fatalf("an MGET carried %d bytes over %d keys, past the %d byte budget: the envelope pass took a bound that was "+
				"not measured on envelopes (keys %v, bytes %v)",
				bytes, backend.keyCounts[index], runtimeLoadBatchBytes, backend.keyCounts, backend.byteCounts)
		}
	}
	for _, view := range loaded.Items {
		if view.Representation != execution.StateRepresentationEnvelope {
			t.Fatalf("view = %s, want every series read from its envelope", view.Representation)
		}
	}
}

// A frame that is there and does not read leaves the envelope to answer, and
// that is counted - as what it is. The count cannot see an envelope outranking
// a frame that reads, because such a frame never reaches the second pass; that
// shape has no reading today and is recorded as a boundary rather than
// assumed away.
func TestAnEnvelopeAnsweringForAnUnreadableFrameIsCounted(t *testing.T) {
	older := execution.ApplyVersion{StateApplyEpoch: 1, EvaluationTime: 30, SlotDigest: "slot-0"}
	newer := applyVersion()
	backend := newPipelineMemoryBackend()
	store := newBatchStore(t, backend, nil)
	identity := seriesIdentity(0)
	envelopeKey, _ := RuntimeStateKeyV2("alarmd", identity)
	framedKey, _ := RuntimeStateKeyV3("alarmd", identity)
	// The frame is unreadable, so the envelope is fetched and compared; a
	// readable frame is never compared against the envelope at all, which is
	// the change this count exists to watch.
	backend.values[framedKey] = []byte("not-json")
	backend.values[envelopeKey], _ = encodeRuntime(seriesMutation(t, identity, newer, 0, "env"), 9)

	loaded, err := store.LoadRuntime(context.Background(), execution.StatePreflightRequest{Contract: frozenRef(),
		Items: []execution.StatePreflightItem{{Identity: identity, ApplyVersion: older}}})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.EnvelopeReads != 1 || loaded.FrameCorruptRescued != 1 {
		t.Fatalf("EnvelopeReads = %d, FrameCorruptRescued = %d, want 1 and 1: the envelope answered for a series whose frame could not",
			loaded.EnvelopeReads, loaded.FrameCorruptRescued)
	}
	// And it is not filed as the older representation still being written:
	// the two read the same on the total above and send a reader to opposite
	// places -- one to a damaged record, the other to a migration to wait out.
	if loaded.EnvelopeAnswered != 0 {
		t.Fatalf("EnvelopeAnswered = %d for a series whose frame was present and corrupt: a damaged record is being "+
			"counted as the migration stock", loaded.EnvelopeAnswered)
	}
	if loaded.Items[0].Representation != execution.StateRepresentationEnvelope {
		t.Fatalf("view = %s, want the envelope: it is the only record that read", loaded.Items[0].Representation)
	}
}
