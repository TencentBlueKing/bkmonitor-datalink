// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// countingStrategySource counts the document reads a reconciler asks of the
// real Legacy source. The reconciler's skip is decided on the change signal
// and the active set, and this is what shows whether it then read anything.
type countingStrategySource struct {
	inner         *controlplane.LegacyRedisStrategySource
	documentReads int
}

func (source *countingStrategySource) ActiveStrategyIDs(ctx context.Context) ([]string, error) {
	return source.inner.ActiveStrategyIDs(ctx)
}

func (source *countingStrategySource) ActiveStrategyIDsWithDigest(ctx context.Context) ([]string, string, error) {
	return source.inner.ActiveStrategyIDsWithDigest(ctx)
}

func (source *countingStrategySource) Strategies(ctx context.Context, ids []string) ([]controlplane.SourceStrategy, error) {
	source.documentReads++
	return source.inner.Strategies(ctx, ids)
}

func (source *countingStrategySource) ChangeSignal(ctx context.Context) (controlplane.SourceChangeSignal, error) {
	return source.inner.ChangeSignal(ctx)
}

type changeGateHarness struct {
	t          *testing.T
	ctx        context.Context
	client     *redis.Client
	prefix     string
	source     *countingStrategySource
	planner    controlplane.PrimaryQueryCompiler
	repository *controlplane.RedisCatalogRepository
	compiler   *strategy.PlanCompiler
	semantics  strategy.StateSemantics
	reconciler *controlplane.SourceReconciler
	documents  []json.RawMessage
	clock      time.Time
}

func newChangeGateHarness(t *testing.T) *changeGateHarness {
	t.Helper()
	ctx := context.Background()
	client := newControlplaneRedis(t)
	documents := realThresholdDocuments(t)
	if err := client.Set(ctx, "bkmonitor.cache.strategy_ids", `[1001, 1002]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	for index, id := range []string{"1001", "1002"} {
		if err := client.Set(ctx, "bkmonitor.cache.strategy_"+id, string(documents[index]), 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:change-gate", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	compiler, semantics := runtimePlanCompiler(t)
	reconciler, err := controlplane.NewSourceReconciler(repository, compiler, semantics)
	if err != nil {
		t.Fatal(err)
	}
	harness := &changeGateHarness{
		t: t, ctx: ctx, client: client, prefix: "alarmd:control:change-gate",
		source:  &countingStrategySource{inner: newRedisStrategySource(t, client)},
		planner: planner, repository: repository, compiler: compiler, semantics: semantics,
		reconciler: reconciler, documents: documents, clock: time.Unix(1_700_000_000, 0),
	}
	if err := reconciler.ConfigureClock(func() time.Time { return harness.clock }); err != nil {
		t.Fatal(err)
	}
	harness.signal(harness.clock.Add(-30 * time.Second))
	return harness
}

// signal writes the change signal the way the cache manager does: the
// integer second its run started.
func (harness *changeGateHarness) signal(at time.Time) {
	harness.t.Helper()
	if err := harness.client.Set(harness.ctx, "bkmonitor.cache.last_updated", strconv.FormatInt(at.Unix(), 10), 0).Err(); err != nil {
		harness.t.Fatal(err)
	}
}

// edit changes one document in place without touching the change signal,
// the way the cache manager's full refresh rewrites content it derives from
// other tables.
func (harness *changeGateHarness) edit(id string, index int, from, to string) {
	harness.t.Helper()
	changed := bytes.Replace(harness.documents[index], []byte(from), []byte(to), 1)
	if bytes.Equal(changed, harness.documents[index]) {
		harness.t.Fatalf("the edit %q -> %q changed nothing; the fixture no longer carries what this test moves", from, to)
	}
	harness.documents[index] = changed
	if err := harness.client.Set(harness.ctx, "bkmonitor.cache.strategy_"+id, string(changed), 0).Err(); err != nil {
		harness.t.Fatal(err)
	}
}

func (harness *changeGateHarness) refresh(
	status controlplane.SourceRefreshStatus,
	mode controlplane.SourceReadMode,
	reason controlplane.SourceReadReason,
	documentReads int,
) controlplane.SourceRefreshResult {
	harness.t.Helper()
	before := harness.source.documentReads
	result, err := harness.reconciler.Refresh(harness.ctx, harness.source, harness.planner)
	if err != nil || result.Status != status || result.ReadMode != mode || result.ReadReason != reason {
		harness.t.Fatalf("Refresh() = (%+v, %v), want status %s read %s/%s", result, err, status, mode, reason)
	}
	if reads := harness.source.documentReads - before; reads != documentReads || (reads == 0) != (result.StrategiesRead == 0) {
		harness.t.Fatalf("round read documents %d times and reported %d strategies read, want %d reads", reads, result.StrategiesRead, documentReads)
	}
	return result
}

// settle takes a fresh repository to the steady state: the first
// observation is published, and the second, which reads again because a
// publication came before it, finds the publication unchanged. Neither may
// reuse an observation.
func (harness *changeGateHarness) settle() controlplane.SourceRefreshResult {
	harness.t.Helper()
	harness.refresh(controlplane.SourceRefreshPublished, controlplane.SourceReadFull, controlplane.SourceReadElected, 1)
	return harness.refresh(controlplane.SourceRefreshUnchanged, controlplane.SourceReadFull, controlplane.SourceReadPending, 1)
}

// settleAfter runs rounds until one finds the publication unchanged. The
// first must read for the given reason, and every later one reads because the
// round before it did not end unchanged. How many rounds that takes is not
// asserted: a change of the active set can take more than one publication
// here, as retention carries a departed strategy's Plan through one
// publication before dropping it.
func (harness *changeGateHarness) settleAfter(reason controlplane.SourceReadReason) controlplane.SourceRefreshResult {
	harness.t.Helper()
	for round := 0; round < 8; round++ {
		want := controlplane.SourceReadPending
		if round == 0 {
			want = reason
		}
		before := harness.source.documentReads
		result, err := harness.reconciler.Refresh(harness.ctx, harness.source, harness.planner)
		if err != nil || result.ReadMode != controlplane.SourceReadFull || result.ReadReason != want ||
			harness.source.documentReads != before+1 {
			harness.t.Fatalf("round %d while settling = (%+v, %v) with %d reads, want a full read for %s", round, result, err, harness.source.documentReads-before, want)
		}
		if result.Status == controlplane.SourceRefreshUnchanged {
			return result
		}
	}
	harness.t.Fatal("the source did not settle within eight rounds")
	return controlplane.SourceRefreshResult{}
}

// Every round used to read every strategy document, whatever the source had
// to say about changes. The Legacy source's publisher moves a change signal
// after each run that changed something, so a round that finds the signal
// and the active set where they were reuses the previous round's observation
// and stands exactly where it stood. The signal moving, the active set
// moving, or the signal being gone each bring the read back.
func TestSourceRefreshReusesItsObservationWhileTheSourceSignalsNoChange(t *testing.T) {
	harness := newChangeGateHarness(t)
	settled := harness.settle()
	if !settled.ChangeSignalPresent || settled.ChangeSignalAgeSeconds != 30 {
		t.Fatalf("settled round signal = present %v age %d, want the signal written 30 s before the clock", settled.ChangeSignalPresent, settled.ChangeSignalAgeSeconds)
	}
	skipped := harness.refresh(controlplane.SourceRefreshUnchanged, controlplane.SourceReadSkipped, controlplane.SourceReadUnchanged, 0)
	if skipped.Publication != settled.Publication || skipped.Observation != settled.Observation ||
		skipped.CompiledStrategies != 0 || skipped.ReusedStrategies != 2 {
		t.Fatalf("skipped round = %+v, want it to stand where the last read stood: %+v", skipped, settled)
	}
	harness.clock = harness.clock.Add(time.Minute)
	if later := harness.refresh(controlplane.SourceRefreshUnchanged, controlplane.SourceReadSkipped, controlplane.SourceReadUnchanged, 0); later.ChangeSignalAgeSeconds != 90 {
		t.Fatalf("signal age after a minute = %d, want 90", later.ChangeSignalAgeSeconds)
	}

	// The publisher ran and moved the signal without changing any document:
	// the round reads, finds nothing new, and the next round may skip again.
	harness.signal(harness.clock)
	if moved := harness.refresh(controlplane.SourceRefreshUnchanged, controlplane.SourceReadFull, controlplane.SourceReadChanged, 1); moved.ChangeSignalAgeSeconds != 0 {
		t.Fatalf("signal age right after it moved = %d, want 0", moved.ChangeSignalAgeSeconds)
	}
	harness.refresh(controlplane.SourceRefreshUnchanged, controlplane.SourceReadSkipped, controlplane.SourceReadUnchanged, 0)

	// The active set shrinks while the signal stays: the round reads.
	if err := harness.client.Set(harness.ctx, "bkmonitor.cache.strategy_ids", `[1001]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	harness.settleAfter(controlplane.SourceReadChanged)
	harness.refresh(controlplane.SourceRefreshUnchanged, controlplane.SourceReadSkipped, controlplane.SourceReadUnchanged, 0)

	// The signal is gone: every round reads, as before the signal was consulted.
	if err := harness.client.Del(harness.ctx, "bkmonitor.cache.last_updated").Err(); err != nil {
		t.Fatal(err)
	}
	if missing := harness.refresh(controlplane.SourceRefreshUnchanged, controlplane.SourceReadFull, controlplane.SourceReadMissing, 1); missing.ChangeSignalPresent {
		t.Fatalf("round without a signal = %+v, want it to say the signal was absent", missing)
	}
	harness.refresh(controlplane.SourceRefreshUnchanged, controlplane.SourceReadFull, controlplane.SourceReadMissing, 1)
}

// The publisher rewrites some content without moving its change signal (the
// conditions it derives from other tables), so a skipped round cannot see
// such a change. That staleness is bounded by a periodic read, six minutes
// after the last one, which sees the change and publishes it.
func TestSourceRefreshSeesAnUnsignalledChangeWithinThePeriodicBound(t *testing.T) {
	harness := newChangeGateHarness(t)
	settled := harness.settle()
	harness.refresh(controlplane.SourceRefreshUnchanged, controlplane.SourceReadSkipped, controlplane.SourceReadUnchanged, 0)
	harness.edit("1002", 1, `"threshold":90`, `"threshold":95`)
	if blind := harness.refresh(controlplane.SourceRefreshUnchanged, controlplane.SourceReadSkipped, controlplane.SourceReadUnchanged, 0); blind.Observation != settled.Observation {
		t.Fatalf("a skipped round observed %s, want the observation it reused %s: an unsignalled change is invisible to it by construction", blind.Observation, settled.Observation)
	}
	harness.clock = harness.clock.Add(6 * time.Minute)
	seen := harness.refresh(controlplane.SourceRefreshPublished, controlplane.SourceReadFull, controlplane.SourceReadPeriodic, 1)
	if seen.Observation == settled.Observation || seen.Publication == settled.Publication {
		t.Fatalf("the periodic read = %+v, want the edited document seen and published", seen)
	}
	after := harness.refresh(controlplane.SourceRefreshUnchanged, controlplane.SourceReadFull, controlplane.SourceReadPending, 1)
	if after.Observation != seen.Observation || after.Publication != seen.Publication {
		t.Fatalf("the round after the periodic read = %+v, want it to find %s published", after, seen.Observation)
	}
	if after.CompiledStrategies != 0 || after.ReusedStrategies != 2 {
		t.Fatalf("round after the periodic read compiled=%d reused=%d, want its compilation reused", after.CompiledStrategies, after.ReusedStrategies)
	}
}

// harnessStrategyIDs is the strategy_ids value newChangeGateHarness stores,
// byte for byte.
const harnessStrategyIDs = `[1001, 1002]`

// state writes the writer's publication statement for the given last_updated
// about the given strategy_ids bytes, which need not be the ones stored.
func (harness *changeGateHarness) state(lastUpdated int64, strategyIDs string) {
	harness.t.Helper()
	sum := sha256.Sum256([]byte(strategyIDs))
	statement := `{"hold_last_good":true,"last_updated":` + strconv.FormatInt(lastUpdated, 10) +
		`,"strategy_ids_sha256":"` + hex.EncodeToString(sum[:]) + `","version":1}`
	if err := harness.client.Set(harness.ctx, "bkmonitor.cache.publication_semantics", statement, 0).Err(); err != nil {
		harness.t.Fatal(err)
	}
}

// publish is one publication by a writer that makes the statement: the
// strategy_ids bytes, the change signal at the given moment, and the
// statement about both.
func (harness *changeGateHarness) publish(at time.Time, strategyIDs string) {
	harness.t.Helper()
	harness.setActiveSet(strategyIDs)
	harness.signal(at)
	harness.state(at.Unix(), strategyIDs)
}

func (harness *changeGateHarness) signalled() int64 {
	harness.t.Helper()
	signalled, err := harness.client.Get(harness.ctx, "bkmonitor.cache.last_updated").Int64()
	if err != nil {
		harness.t.Fatal(err)
	}
	return signalled
}

// observed is the reconciler's observed snapshot, which has to exist.
func (harness *changeGateHarness) observed() controlplane.ObservedSnapshot {
	harness.t.Helper()
	observed, ok := harness.reconciler.ObservedSnapshot()
	if !ok {
		harness.t.Fatal("ObservedSnapshot() = none, want the last read")
	}
	return observed
}

// The observation carries the writer's statement as it was read with the
// observation's own change signal. A writer that later publishes a change
// without restating it - an older writer after a rollback - leaves an
// observation that no longer carries it, although the strategy_ids it stored
// are the very bytes the statement names.
func TestTheObservedSnapshotCarriesTheWritersStatementReadWithIt(t *testing.T) {
	harness := newChangeGateHarness(t)
	harness.state(harness.signalled(), harnessStrategyIDs)
	harness.settle()
	if observed := harness.observed(); !observed.HoldsLastGood {
		t.Fatalf("ObservedSnapshot() = %+v, want the statement read with its signal", observed)
	}

	// An older writer publishes a change: the signal moves, the statement stays behind.
	harness.clock = harness.clock.Add(time.Minute)
	harness.signal(harness.clock)
	harness.refresh(controlplane.SourceRefreshUnchanged, controlplane.SourceReadFull, controlplane.SourceReadChanged, 1)
	if observed := harness.observed(); observed.HoldsLastGood {
		t.Fatalf("ObservedSnapshot() after a change without the statement = %+v, want no statement", observed)
	}

	// The writer restates it for the new signal; the next read carries it again.
	harness.clock = harness.clock.Add(time.Minute)
	harness.publish(harness.clock, harnessStrategyIDs)
	harness.refresh(controlplane.SourceRefreshUnchanged, controlplane.SourceReadFull, controlplane.SourceReadChanged, 1)
	if observed := harness.observed(); !observed.HoldsLastGood {
		t.Fatalf("ObservedSnapshot() after the statement was restated = %+v, want it", observed)
	}
}

// An older writer can also rewrite strategy_ids in place - its full refresh
// drops a strategy that failed to publish - without moving last_updated. The
// statement it leaves behind still matches the signal, and is about the list
// before the drop: the observation of the rewritten list does not carry it.
func TestAStatementDoesNotCoverStrategyIDsRewrittenInPlace(t *testing.T) {
	harness := newChangeGateHarness(t)
	signalled := harness.signalled()
	harness.state(signalled, harnessStrategyIDs)
	harness.settle()
	if observed := harness.observed(); !observed.HoldsLastGood || len(observed.Strategies) != 2 {
		t.Fatalf("ObservedSnapshot() = %+v, want both strategies under the statement", observed)
	}

	harness.setActiveSet(`[1001]`)
	harness.settleAfter(controlplane.SourceReadChanged)
	if harness.signalled() != signalled {
		t.Fatal("the in-place rewrite moved the signal; this test is about one that does not")
	}
	if observed := harness.observed(); observed.HoldsLastGood || len(observed.Strategies) != 1 {
		t.Fatalf("ObservedSnapshot() of a list rewritten in place = %+v, want the one strategy without the statement", observed)
	}

	// A writer that makes the statement publishes about the list as it now is.
	// Under it 1002, which was serving the grace, is gone and leaves at once,
	// so the round is a change and not a restatement.
	harness.clock = harness.clock.Add(time.Minute)
	harness.publish(harness.clock, `[1001]`)
	settled := harness.settleAfter(controlplane.SourceReadChanged)
	if observed := harness.observed(); !observed.HoldsLastGood || len(observed.Strategies) != 1 {
		t.Fatalf("ObservedSnapshot() after the statement about this list = %+v, want it", observed)
	}
	if objects := settled.Composition.Objects; objects[controlplane.DispositionPendingRemoval] != 0 || objects[controlplane.DispositionAccepted] != 1 {
		t.Fatalf("composition under the statement = %v, want 1001 alone and nothing under the grace", objects)
	}
}

// The statement names bytes, not ids. A digest of the same ids spelled
// another way is a statement about a value the store does not hold.
func TestAStatementNamesTheStoredBytesNotTheIDs(t *testing.T) {
	harness := newChangeGateHarness(t)
	harness.state(harness.signalled(), `[1001,1002]`)
	harness.settle()
	if observed := harness.observed(); observed.HoldsLastGood {
		t.Fatalf("ObservedSnapshot() under a digest of other bytes = %+v, want no statement", observed)
	}
	harness.clock = harness.clock.Add(time.Minute)
	harness.publish(harness.clock, harnessStrategyIDs)
	harness.refresh(controlplane.SourceRefreshUnchanged, controlplane.SourceReadFull, controlplane.SourceReadChanged, 1)
	if observed := harness.observed(); !observed.HoldsLastGood {
		t.Fatalf("ObservedSnapshot() under the digest of the stored bytes = %+v, want it", observed)
	}
}

// strategySourceOnly is a StrategySource and nothing more: no change signal,
// and no name for the bytes of its active set.
type strategySourceOnly struct{ inner *countingStrategySource }

func (source strategySourceOnly) ActiveStrategyIDs(ctx context.Context) ([]string, error) {
	return source.inner.ActiveStrategyIDs(ctx)
}

func (source strategySourceOnly) Strategies(ctx context.Context, ids []string) ([]controlplane.SourceStrategy, error) {
	return source.inner.Strategies(ctx, ids)
}

// unnamedActiveSetSource reads the change signal and the statement with it,
// and cannot name the bytes of its active set.
type unnamedActiveSetSource struct{ strategySourceOnly }

func (source unnamedActiveSetSource) ChangeSignal(ctx context.Context) (controlplane.SourceChangeSignal, error) {
	return source.inner.ChangeSignal(ctx)
}

// A source that cannot name the bytes of its active set has no statement
// about it, whatever its publisher wrote: there is nothing to hold the
// statement to.
func TestASourceThatCannotNameItsActiveSetHasNoStatement(t *testing.T) {
	harness := newChangeGateHarness(t)
	harness.state(harness.signalled(), harnessStrategyIDs)
	for name, source := range map[string]controlplane.StrategySource{
		"without a change signal":                strategySourceOnly{harness.source},
		"with the change signal and a statement": unnamedActiveSetSource{strategySourceOnly{harness.source}},
	} {
		result, err := harness.reconciler.Refresh(harness.ctx, source, harness.planner)
		if err != nil || result.ReadMode != controlplane.SourceReadFull {
			t.Fatalf("Refresh() of a source %s = (%+v, %v), want a full read", name, result, err)
		}
		if observed := harness.observed(); observed.HoldsLastGood {
			t.Fatalf("ObservedSnapshot() of a source %s = %+v, want no statement", name, observed)
		}
	}
	// The writer publishes again, so the round reads whatever the round
	// before it was.
	harness.clock = harness.clock.Add(time.Minute)
	harness.publish(harness.clock, harnessStrategyIDs)
	harness.refresh(controlplane.SourceRefreshUnchanged, controlplane.SourceReadFull, controlplane.SourceReadChanged, 1)
	if observed := harness.observed(); !observed.HoldsLastGood {
		t.Fatalf("ObservedSnapshot() of the source that names its bytes = %+v, want the statement", observed)
	}
}

// publishingSource lands a publication between the change signal a round
// reads, with the statement, and the active set its cycle reads.
type publishingSource struct {
	*countingStrategySource
	between func()
}

func (source *publishingSource) ChangeSignal(ctx context.Context) (controlplane.SourceChangeSignal, error) {
	signal, err := source.countingStrategySource.ChangeSignal(ctx)
	if source.between != nil {
		source.between()
		source.between = nil
	}
	return signal, err
}

// The round reads the statement with its signal, before the cycle reads the
// active set. A publication in between that changes strategy_ids leaves the
// round holding a statement about the list before it: not taken. One that
// leaves strategy_ids byte for byte as they were leaves a statement that is
// still about exactly the list the round read, and it is taken.
func TestAStatementReadBeforeAPublicationDoesNotCoverWhatItChanged(t *testing.T) {
	harness := newChangeGateHarness(t)
	harness.state(harness.signalled(), harnessStrategyIDs)
	harness.settle()
	refresh := func(between func()) controlplane.ObservedSnapshot {
		t.Helper()
		source := &publishingSource{countingStrategySource: harness.source, between: between}
		result, err := harness.reconciler.Refresh(harness.ctx, source, harness.planner)
		if err != nil || result.ReadMode != controlplane.SourceReadFull || source.between != nil {
			t.Fatalf("Refresh() = (%+v, %v), want a full read with the publication landed in it", result, err)
		}
		return harness.observed()
	}

	// The same list: the writer publishes again, with a document changed.
	harness.clock = harness.clock.Add(time.Minute)
	harness.publish(harness.clock, harnessStrategyIDs)
	unchanged := refresh(func() {
		harness.edit("1002", 1, `"threshold":90`, `"threshold":95`)
		harness.publish(harness.clock.Add(time.Second), harnessStrategyIDs)
	})
	if !unchanged.HoldsLastGood || len(unchanged.Strategies) != 2 {
		t.Fatalf("ObservedSnapshot() across a publication of the same list = %+v, want the statement", unchanged)
	}

	// Another list: strategy 1002 leaves it in the publication in between.
	harness.clock = harness.clock.Add(time.Minute)
	harness.publish(harness.clock, harnessStrategyIDs)
	changed := refresh(func() { harness.publish(harness.clock.Add(time.Second), `[1001]`) })
	if changed.HoldsLastGood || len(changed.Strategies) != 1 {
		t.Fatalf("ObservedSnapshot() across a publication of another list = %+v, want no statement", changed)
	}

	// The next round reads that publication's own statement with its signal.
	if next := refresh(nil); !next.HoldsLastGood || len(next.Strategies) != 1 {
		t.Fatalf("ObservedSnapshot() of the next round = %+v, want the statement about the list it read", next)
	}
}
