// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package state

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// The second pass reports which of the five it found, and one round holds all
// of them at once.
//
// The total on its own cannot say when the migration is over: a series with no
// record at all has no frame either, so it goes through the second pass and
// keeps going through it for ever. On the release that first carried the total
// a round read 131 of 256 that way, and nothing in the number said how many of
// the 131 were the older representation. The two corruption counts were
// invisible in it for the same reason -- a damaged frame the older record
// rescued read exactly like a new series arriving, and they send a reader to
// opposite places.
//
// One round with all five shapes present, because the failure this pins is
// miscounting one shape as another: a case with a single shape in it passes
// against a counter that files everything under one bucket.
func TestTheSecondPassSaysWhichOfTheFourItFound(t *testing.T) {
	version := applyVersion()
	backend := newPipelineMemoryBackend()
	store := newBatchStore(t, backend, nil)

	const (
		stock  = iota // no frame, the envelope answers: the migration stock
		fresh         // no frame, no envelope either: a series with no record yet
		rotten        // no frame, the envelope has bytes that do not read
		saved         // frame present and corrupt, the envelope answers
		lost          // frame present and corrupt, nothing answers
		framed        // a healthy frame, which never reaches the second pass
		shapes
	)
	items := make([]execution.StatePreflightItem, shapes)
	for shape := range items {
		identity := seriesIdentity(shape)
		items[shape] = execution.StatePreflightItem{Identity: identity, ApplyVersion: version}
		envelopeKey, _ := RuntimeStateKeyV2("alarmd", identity)
		framedKey, _ := RuntimeStateKeyV3("alarmd", identity)
		switch shape {
		case stock:
			backend.values[envelopeKey], _ = encodeRuntime(seriesMutation(t, identity, version, 0, "env"), 7)
		case fresh:
			// Nothing written at all.
		case rotten:
			backend.values[envelopeKey] = []byte("not-an-envelope")
		case saved:
			backend.values[framedKey] = []byte("not-a-frame")
			backend.values[envelopeKey], _ = encodeRuntime(seriesMutation(t, identity, version, 0, "env"), 7)
		case lost:
			backend.values[framedKey] = []byte("not-a-frame")
		case framed:
			backend.values[framedKey], _ = encodeRuntimePacked(seriesMutation(t, identity, version, 0, ""), 3)
		}
	}

	loaded, err := store.LoadRuntime(context.Background(), execution.StatePreflightRequest{Contract: frozenRef(), Items: items})
	if err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string]struct{ have, want int }{
		"EnvelopeAnswered":    {loaded.EnvelopeAnswered, 1},
		"EnvelopeCorrupt":     {loaded.EnvelopeCorrupt, 1},
		"NoRecordYet":         {loaded.NoRecordYet, 1},
		"FrameCorruptRescued": {loaded.FrameCorruptRescued, 1},
		"FrameCorruptLost":    {loaded.FrameCorruptLost, 1},
	} {
		if got.have != got.want {
			t.Errorf("%s = %d, want %d (all five shapes are in this round exactly once)", name, got.have, got.want)
		}
	}
	// The five account for the whole of the second pass, which is what makes
	// this the completeness guard: a sixth shape added without a bucket lands
	// in none of them and the sum falls short, where every per-bucket case
	// above would still pass. The healthy frame is answered in the first pass
	// and must not appear in any of them.
	if loaded.Unclassified != 0 {
		t.Fatalf("Unclassified = %d: a shape reached the split that none of the five names, so they are no longer a "+
			"partition and the total no longer cross-checks them", loaded.Unclassified)
	}
	if sum := loaded.EnvelopeAnswered + loaded.EnvelopeCorrupt + loaded.NoRecordYet + loaded.FrameCorruptRescued +
		loaded.FrameCorruptLost + loaded.Unclassified; sum != loaded.EnvelopeReads {
		t.Fatalf("the five sum to %d against %d series that needed the second read: a shape is being counted twice "+
			"or not at all", sum, loaded.EnvelopeReads)
	}
	if loaded.EnvelopeReads != shapes-1 {
		t.Fatalf("EnvelopeReads = %d, want %d: the healthy frame answered in the first pass and should not have "+
			"reached the second", loaded.EnvelopeReads, shapes-1)
	}
}

// The count that must reach zero is not moved by the ones that never do.
//
// This is the reading the migration is declared over on, so it gets its own
// case: a deployment that is done migrating still creates series for ever, and
// a bucket that counted those would never let anyone say the compatibility
// read can go.
func TestANewSeriesDoesNotCountAsTheOlderRepresentation(t *testing.T) {
	version := applyVersion()
	backend := newPipelineMemoryBackend()
	store := newBatchStore(t, backend, nil)
	items := make([]execution.StatePreflightItem, 3)
	for index := range items {
		items[index] = execution.StatePreflightItem{Identity: seriesIdentity(index), ApplyVersion: version}
	}

	loaded, err := store.LoadRuntime(context.Background(), execution.StatePreflightRequest{Contract: frozenRef(), Items: items})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.EnvelopeAnswered != 0 {
		t.Fatalf("EnvelopeAnswered = %d on a Query Group that has never written state: the migration could never be "+
			"declared over on a deployment that creates series", loaded.EnvelopeAnswered)
	}
	if loaded.NoRecordYet != len(items) || loaded.EnvelopeReads != len(items) {
		t.Fatalf("NoRecordYet = %d, EnvelopeReads = %d, want %d each: these series went through the second pass and "+
			"are the reason the total cannot be the indicator", loaded.NoRecordYet, loaded.EnvelopeReads, len(items))
	}
}

// failAfterBackend answers the first MGET and refuses the ones after it, so a
// preflight's first pass succeeds and its second pass does not.
type failAfterBackend struct {
	*pipelineMemoryBackend
	calls int
}

func (backend *failAfterBackend) MGet(ctx context.Context, keys []string) ([][]byte, error) {
	backend.calls++
	if backend.calls > 1 {
		return nil, errors.New("state: injected second-pass read failure")
	}
	return backend.pipelineMemoryBackend.MGet(ctx, keys)
}

// The five and the total differ by exactly the series whose read failed.
//
// This is the identity the total is kept for, and the only statement about
// these counts that holds on every round: a batch whose read fails classifies
// every one of its items as a failure and never reaches the split, so those
// series are in EnvelopeReads and in none of the five. Without a case for it
// the identity lives only in a comment, and a comment is what the last wrong
// claim about these counts was.
//
// It pins the other term of that identity. The completeness guard is the sum
// in the case above, where every read comes back and the five must account for
// all of them; this one covers the round where they must not.
func TestTheFiveAndTheTotalDifferByTheReadsThatFailed(t *testing.T) {
	version := applyVersion()
	backend := &failAfterBackend{pipelineMemoryBackend: newPipelineMemoryBackend()}
	store := newBatchStore(t, backend, nil)
	items := make([]execution.StatePreflightItem, 3)
	for index := range items {
		identity := seriesIdentity(index)
		items[index] = execution.StatePreflightItem{Identity: identity, ApplyVersion: version}
		envelopeKey, _ := RuntimeStateKeyV2("alarmd", identity)
		backend.values[envelopeKey], _ = encodeRuntime(seriesMutation(t, identity, version, 0, "env"), 7)
	}

	loaded, err := store.LoadRuntime(context.Background(), execution.StatePreflightRequest{Contract: frozenRef(), Items: items})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.EnvelopeReads != len(items) {
		t.Fatalf("EnvelopeReads = %d, want %d: every series had no frame and went to the second pass",
			loaded.EnvelopeReads, len(items))
	}
	sum := loaded.EnvelopeAnswered + loaded.EnvelopeCorrupt + loaded.NoRecordYet +
		loaded.FrameCorruptRescued + loaded.FrameCorruptLost
	if sum != 0 {
		t.Fatalf("the five sum to %d on a round whose second pass never came back: a series that was not read "+
			"cannot have been classified", sum)
	}
	// And the series say so, rather than reading as a finished migration.
	for index, view := range loaded.Items {
		if view.Status != execution.StateRetryableIO {
			t.Fatalf("series %d status = %q, want a retryable read: the envelope was never fetched", index, view.Status)
		}
	}
}

// slowBackend answers every MGET after a fixed delay, the way a store with a
// round trip does.
type slowBackend struct {
	*pipelineMemoryBackend
	delay time.Duration
	calls int
}

func (backend *slowBackend) MGet(ctx context.Context, keys []string) ([][]byte, error) {
	backend.calls++
	time.Sleep(backend.delay)
	return backend.pipelineMemoryBackend.MGet(ctx, keys)
}

// The preflight's time is split where the store's reads end: the reads,
// round trip included, are fetch; turning their bytes into views is decode.
// A caller that did not ask for the split gets the same result without it.
func TestThePreflightSplitsItsTimeAtTheStoreRead(t *testing.T) {
	version := applyVersion()
	backend := &slowBackend{pipelineMemoryBackend: newPipelineMemoryBackend(), delay: 20 * time.Millisecond}
	store := newBatchStore(t, backend, nil)
	items := make([]execution.StatePreflightItem, 3)
	for index := range items {
		identity := seriesIdentity(index)
		items[index] = execution.StatePreflightItem{Identity: identity, ApplyVersion: version}
		envelopeKey, _ := RuntimeStateKeyV2("alarmd", identity)
		backend.values[envelopeKey], _ = encodeRuntime(seriesMutation(t, identity, version, 0, "env"), 7)
	}
	request := execution.StatePreflightRequest{Contract: frozenRef(), Items: items}
	ctx, timing := execution.WithPreflightTiming(context.Background())
	timed, err := store.LoadRuntime(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Duration(backend.calls) * backend.delay; backend.calls == 0 || timing.Fetch < want {
		t.Fatalf("fetch %s over %d reads, want at least %s", timing.Fetch, backend.calls, want)
	}
	if timing.Decode <= 0 || timing.Decode >= timing.Fetch {
		t.Fatalf("decode %s, want some time and less than the store's %s", timing.Decode, timing.Fetch)
	}
	untimed, err := store.LoadRuntime(context.Background(), request)
	if err != nil || len(untimed.Items) != len(timed.Items) || untimed.LoadedBytes != timed.LoadedBytes {
		t.Fatalf("untimed %+v err %v, want the same read", untimed, err)
	}
}
