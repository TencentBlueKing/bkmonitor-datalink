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

// settle takes a fresh repository to the steady state: the first observation
// is a candidate, the second confirms and publishes it, and the third finds
// the publication unchanged. None of the three may reuse an observation.
func (harness *changeGateHarness) settle() controlplane.SourceRefreshResult {
	harness.t.Helper()
	harness.refresh(controlplane.SourceRefreshPendingConfirmation, controlplane.SourceReadFull, controlplane.SourceReadElected, 1)
	harness.refresh(controlplane.SourceRefreshPublished, controlplane.SourceReadFull, controlplane.SourceReadPending, 1)
	return harness.refresh(controlplane.SourceRefreshUnchanged, controlplane.SourceReadFull, controlplane.SourceReadPending, 1)
}

// settleAfter runs rounds until one finds the publication unchanged. The
// first must read for the given reason, and every later one reads because the
// round before it did not end unchanged. How many rounds that takes is not
// asserted: a change of the active set goes through more than one
// confirmation here, as retention carries a departed strategy's Plan through
// one publication before dropping it.
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
// after the last one, which sees the change and takes it through the usual
// confirmation.
func TestSourceRefreshSeesAnUnsignalledChangeWithinThePeriodicBound(t *testing.T) {
	harness := newChangeGateHarness(t)
	settled := harness.settle()
	harness.refresh(controlplane.SourceRefreshUnchanged, controlplane.SourceReadSkipped, controlplane.SourceReadUnchanged, 0)
	harness.edit("1002", 1, `"threshold":90`, `"threshold":95`)
	if blind := harness.refresh(controlplane.SourceRefreshUnchanged, controlplane.SourceReadSkipped, controlplane.SourceReadUnchanged, 0); blind.Observation != settled.Observation {
		t.Fatalf("a skipped round observed %s, want the observation it reused %s: an unsignalled change is invisible to it by construction", blind.Observation, settled.Observation)
	}
	harness.clock = harness.clock.Add(6 * time.Minute)
	seen := harness.refresh(controlplane.SourceRefreshPendingConfirmation, controlplane.SourceReadFull, controlplane.SourceReadPeriodic, 1)
	if seen.Observation == settled.Observation {
		t.Fatalf("the periodic read observed %s again, want the edited document seen", seen.Observation)
	}
	published := harness.refresh(controlplane.SourceRefreshPublished, controlplane.SourceReadFull, controlplane.SourceReadPending, 1)
	if published.Observation != seen.Observation {
		t.Fatalf("confirmed %s, want the periodic read's observation %s", published.Observation, seen.Observation)
	}
	if published.CompiledStrategies != 0 || published.ReusedStrategies != 2 {
		t.Fatalf("confirming round compiled=%d reused=%d, want the periodic read's compilation reused", published.CompiledStrategies, published.ReusedStrategies)
	}
}

// Confirmation is two independent reads of the source. A round that follows
// a candidate reads even when the signal and the active set have not moved,
// so what it confirms is what the source holds now and not what an earlier
// round remembered; here the source moved in between and the candidate is
// replaced rather than published.
func TestSourceRefreshConfirmsOnlyWhatItReadTwice(t *testing.T) {
	harness := newChangeGateHarness(t)
	first := harness.refresh(controlplane.SourceRefreshPendingConfirmation, controlplane.SourceReadFull, controlplane.SourceReadElected, 1)
	harness.edit("1002", 1, `"threshold":90`, `"threshold":95`)
	second := harness.refresh(controlplane.SourceRefreshPendingConfirmation, controlplane.SourceReadFull, controlplane.SourceReadPending, 1)
	if second.Observation == first.Observation {
		t.Fatalf("the round after a candidate observed %s again, want the source read anew", second.Observation)
	}
	third := harness.refresh(controlplane.SourceRefreshPublished, controlplane.SourceReadFull, controlplane.SourceReadPending, 1)
	if third.Observation != second.Observation {
		t.Fatalf("published %s, want the twice-read observation %s", third.Observation, second.Observation)
	}
}
