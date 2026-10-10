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
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

func (f *rebuildFixture) loseHeader(t *testing.T) {
	t.Helper()
	if err := f.client.Del(context.Background(), f.prefix+":activation_header").Err(); err != nil {
		t.Fatal(err)
	}
}

func (f *rebuildFixture) headerNow(t *testing.T) (string, bool) {
	t.Helper()
	header, err := f.client.Get(context.Background(), f.prefix+":activation_header").Result()
	if errors.Is(err, redis.Nil) {
		return "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return header, true
}

func (f *rebuildFixture) activeSetKey(t *testing.T) string {
	t.Helper()
	keys, err := f.client.Keys(context.Background(), f.prefix+":active_qg_set:*").Result()
	if err != nil || len(keys) != 1 {
		t.Fatalf("active set keys = %v (%v), want one", keys, err)
	}
	return keys[0]
}

// The header lost and the body kept: the next activation writes the header
// back as the body describes it and goes on. Before the header was written
// back every cutover was refused on every round - the guarded write compares
// the header it read with one that is not there - and so was a restarted
// leader's.
func TestALostHeaderIsWrittenBackByTheNextActivationAndTheCutoverGoesOn(t *testing.T) {
	f := newRebuildFixture(t, "header-lost-cutover")
	ctx := context.Background()
	f.loseHeader(t)
	next, _, err := f.repository.PublishCatalog(ctx, validCatalog(t, 81))
	if err != nil {
		t.Fatal(err)
	}
	state, err := f.reconciler.Ensure(ctx, next.Publication)
	if err != nil {
		t.Fatalf("activation with the header lost: %v", err)
	}
	if state.Current != next.Publication {
		t.Fatalf("activated %+v, want the new publication %+v", state.Current, next.Publication)
	}
	header, ok := f.headerNow(t)
	if !ok || header == f.header || !strings.HasSuffix(header, "|-") {
		t.Fatalf("header after the cutover = %q (present %v), want a new one with no pending publication", header, ok)
	}
	reading := f.repository.ActivationHeaderReading()
	if reading.Missing || reading.Rebuilds[controlplane.ActivationHeaderRebuilt] != 1 {
		t.Fatalf("reading = %+v, want one rebuild and the header no longer missing", reading)
	}
}

// A renewal names a lost header rather than reading it as another writer's
// cutover, and renews once the header is back: the active set's expiry moves
// again.
func TestARenewalNamesALostHeaderAndRenewsOnceItIsWrittenBack(t *testing.T) {
	f := newRebuildFixture(t, "header-lost-renewal")
	ctx := context.Background()
	active := f.activeSetKey(t)
	if err := f.client.PExpire(ctx, active, 5*time.Second).Err(); err != nil {
		t.Fatal(err)
	}
	f.loseHeader(t)
	err := f.repository.RenewCurrentActivationObjects(ctx)
	if !errors.Is(err, controlplane.ErrActivationHeaderMissing) || errors.Is(err, controlplane.ErrActivationConflict) {
		t.Fatalf("renewal with the header lost = %v, want header missing and not a conflict", err)
	}
	if ttl := f.client.PTTL(ctx, active).Val(); ttl > 5*time.Second {
		t.Fatalf("the refused renewal moved the active set's expiry to %v", ttl)
	}
	outcome, err := f.repository.RebuildActivationHeader(ctx)
	if err != nil || outcome != controlplane.ActivationHeaderRebuilt {
		t.Fatalf("rebuild = %s %v", outcome, err)
	}
	if header, ok := f.headerNow(t); !ok || header != f.header {
		t.Fatalf("header written back = %q, want the one it had, %q", header, f.header)
	}
	if err := f.repository.RenewCurrentActivationObjects(ctx); err != nil {
		t.Fatalf("renewal after the header is back: %v", err)
	}
	if ttl := f.client.PTTL(ctx, active).Val(); ttl <= 5*time.Second {
		t.Fatalf("active set expiry after the renewal = %v, want it moved to the catalog TTL", ttl)
	}
	reading := f.repository.ActivationHeaderReading()
	if reading.RenewalConflicts[controlplane.ActivationRenewalHeaderMissing] != 1 ||
		reading.RenewalConflicts[controlplane.ActivationRenewalHeaderMoved] != 0 {
		t.Fatalf("renewal conflicts = %v, want one header_missing", reading.RenewalConflicts)
	}
}

// A header another cutover moved is still a conflict, counted under its own
// name, and is not written back over.
func TestARenewalUnderAMovedHeaderIsAConflictByName(t *testing.T) {
	f := newRebuildFixture(t, "header-moved-renewal")
	ctx := context.Background()
	if err := f.client.Set(ctx, f.prefix+":activation_header", f.header+"x", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := f.repository.RenewCurrentActivationObjects(ctx); !errors.Is(err, controlplane.ErrActivationConflict) {
		t.Fatalf("renewal under a moved header = %v, want a conflict", err)
	}
	if outcome, err := f.repository.RebuildActivationHeader(ctx); err != nil || outcome != controlplane.ActivationHeaderRebuildNotNeeded {
		t.Fatalf("rebuild with a header present = %s %v, want not needed", outcome, err)
	}
	if header, _ := f.headerNow(t); header != f.header+"x" {
		t.Fatalf("header = %q, want the other writer's left alone", header)
	}
	if got := f.repository.ActivationHeaderReading().RenewalConflicts[controlplane.ActivationRenewalHeaderMoved]; got != 1 {
		t.Fatalf("header_moved = %d, want 1", got)
	}
}

// A cutover a later build left unfinished is on the body, not on the header:
// with the header lost mid-cutover, the header is written back as it was and
// the first activation round finishes the cutover as it would have.
func TestAHeaderLostMidCutoverIsWrittenBackAndTheCutoverFinishes(t *testing.T) {
	fixture := newCutoverFixture(t, "alarmd:control:header-lost-mid-cutover")
	_, second, state := unfinishedCutover(t, fixture)
	if err := fixture.client.Del(fixture.ctx, fixture.prefix+":activation_header").Err(); err != nil {
		t.Fatal(err)
	}
	*fixture.now = time.Unix(180, 0)
	finished, err := fixture.reconciler.Ensure(fixture.ctx, second.Publication)
	if err != nil {
		t.Fatalf("finishing the cutover with the header lost: %v", err)
	}
	if finished.CutoverProgress != nil || finished.Current != second.Publication || finished.RecordRevision != state.RecordRevision+1 {
		t.Fatalf("finished activation = %+v", finished)
	}
}

// Only the Control Leader writes the header back, from its activation and its
// renewal. Every reader keeps reading the body, and reading never writes.
func TestReadingAnActivationWithoutItsHeaderWritesNothing(t *testing.T) {
	f := newRebuildFixture(t, "header-lost-readers")
	ctx := context.Background()
	f.loseHeader(t)
	head, err := f.repository.LoadActivationHead(ctx)
	if err != nil || head.Current != f.publication {
		t.Fatalf("a reader's head read = %+v %v, want the body's", head.Current, err)
	}
	if _, err := f.repository.LoadActivation(ctx); err != nil {
		t.Fatalf("the leader's read = %v", err)
	}
	follower, err := controlplane.NewRedisCatalogRepository(f.client, f.prefix, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := follower.LoadActivationHead(ctx); err != nil {
		t.Fatalf("a follower's head read = %v", err)
	}
	if _, ok := f.headerNow(t); ok {
		t.Fatal("a read wrote the header back")
	}
	if f.repository.ActivationHeaderReading().Missing {
		t.Fatal("a read set the leader's standing")
	}
}

// The body changing between the read and the write wins: the header is not
// written from a body that is no longer there.
func TestAHeaderIsNotWrittenFromABodyThatChanged(t *testing.T) {
	f := newRebuildFixture(t, "header-body-changed")
	ctx := context.Background()
	f.loseHeader(t)
	other := redis.NewClient(&redis.Options{Addr: f.client.Options().Addr})
	defer other.Close()
	f.client.AddHook(changeBeforeHeaderRebuild{client: other, key: f.prefix + ":activation", value: string(f.body) + " "})
	outcome, err := f.repository.RebuildActivationHeader(ctx)
	if err != nil || outcome != controlplane.ActivationHeaderRebuildConflict {
		t.Fatalf("rebuild over a changed body = %s %v, want a conflict", outcome, err)
	}
	if _, ok := f.headerNow(t); ok {
		t.Fatal("a header was written over a body that changed")
	}
	reading := f.repository.ActivationHeaderReading()
	if !reading.Missing || reading.LastRebuild != controlplane.ActivationHeaderRebuildConflict {
		t.Fatalf("reading = %+v, want the header still missing after a conflict", reading)
	}
}

type changeBeforeHeaderRebuild struct {
	client     *redis.Client
	key, value string
}

func (hook changeBeforeHeaderRebuild) BeforeProcess(ctx context.Context, cmd redis.Cmder) (context.Context, error) {
	if args := cmd.Args(); len(args) > 1 && strings.EqualFold(cmd.Name(), "eval") {
		if script, ok := args[1].(string); ok && strings.Contains(script, "redis.call('EXISTS', KEYS[1]) == 1") {
			if err := hook.client.Set(ctx, hook.key, hook.value, 0).Err(); err != nil {
				return ctx, err
			}
		}
	}
	return ctx, nil
}
func (changeBeforeHeaderRebuild) AfterProcess(context.Context, redis.Cmder) error { return nil }
func (changeBeforeHeaderRebuild) BeforeProcessPipeline(ctx context.Context, _ []redis.Cmder) (context.Context, error) {
	return ctx, nil
}
func (changeBeforeHeaderRebuild) AfterProcessPipeline(context.Context, []redis.Cmder) error {
	return nil
}

// A body naming a pending publication came from a writer this build does not
// know; the header is not guessed, and the refusal says why.
func TestABodyNamingAPendingPublicationGetsNoHeader(t *testing.T) {
	f := newRebuildFixture(t, "header-body-pending")
	ctx := context.Background()
	state, err := f.repository.LoadActivationHead(ctx)
	if err != nil {
		t.Fatal(err)
	}
	next, _, err := f.repository.PublishCatalog(ctx, validCatalog(t, 82))
	if err != nil {
		t.Fatal(err)
	}
	pending := next.Publication
	state.Pending = &pending
	if err := controlplane.WriteActivationForTest(ctx, f.repository, state); err != nil {
		t.Fatal(err)
	}
	f.loseHeader(t)
	outcome, err := f.repository.RebuildActivationHeader(ctx)
	if outcome != controlplane.ActivationHeaderRebuildBodyPending || !errors.Is(err, controlplane.ErrActivationHeaderRebuildRefused) {
		t.Fatalf("rebuild over a pending body = %s %v", outcome, err)
	}
	if _, ok := f.headerNow(t); ok {
		t.Fatal("a header was guessed for a pending body")
	}
}

// With the header and the body both gone there is nothing to describe: the
// rebuild writes nothing and counts nothing, and what follows is the first
// activation's case, as before (TestAMissingHeaderIsNotABodyMissing).
func TestWithoutAHeaderOrABodyNoHeaderIsWritten(t *testing.T) {
	f := newRebuildFixture(t, "header-and-body-gone")
	ctx := context.Background()
	if err := f.client.Del(ctx, f.prefix+":activation", f.prefix+":activation_header").Err(); err != nil {
		t.Fatal(err)
	}
	outcome, err := f.repository.RebuildActivationHeader(ctx)
	if err != nil || outcome != controlplane.ActivationHeaderRebuildNotNeeded {
		t.Fatalf("rebuild with nothing there = %s %v", outcome, err)
	}
	if _, ok := f.headerNow(t); ok {
		t.Fatal("a header was written with no body")
	}
	if reading := f.repository.ActivationHeaderReading(); reading.Missing || reading.Rebuilds[controlplane.ActivationHeaderRebuilt] != 0 {
		t.Fatalf("reading = %+v, want nothing counted", reading)
	}
}

// A header another writer puts down between the read and the write wins:
// nothing is written over it.
func TestAHeaderThatAppearedDuringTheRebuildIsLeftAlone(t *testing.T) {
	f := newRebuildFixture(t, "header-appeared")
	ctx := context.Background()
	f.loseHeader(t)
	other := redis.NewClient(&redis.Options{Addr: f.client.Options().Addr})
	defer other.Close()
	f.client.AddHook(changeBeforeHeaderRebuild{client: other, key: f.prefix + ":activation_header", value: "another-writer"})
	outcome, err := f.repository.RebuildActivationHeader(ctx)
	if err != nil || outcome != controlplane.ActivationHeaderRebuildConflict {
		t.Fatalf("rebuild with a header appearing = %s %v, want a conflict", outcome, err)
	}
	if header, _ := f.headerNow(t); header != "another-writer" {
		t.Fatalf("header = %q, want the other writer's", header)
	}
	if !f.repository.ActivationHeaderReading().Missing {
		t.Fatal("after the conflict the header is not known to be back yet; the standing cleared early")
	}
	// The next round finds a header and has nothing to do: the standing clears.
	if outcome, err := f.repository.RebuildActivationHeader(ctx); err != nil || outcome != controlplane.ActivationHeaderRebuildNotNeeded {
		t.Fatalf("rebuild with the header back = %s %v", outcome, err)
	}
	if f.repository.ActivationHeaderReading().Missing {
		t.Fatal("the header is back and the standing still says missing")
	}
}

// A renewal that finds the header ends whatever this process last saw of it
// missing: after a rebuild conflicted with another writer putting the header
// back, the standing clears on the next renewal rather than on the next
// activation, which on an unchanged source may be a long way off.
func TestARenewalThatFindsTheHeaderClearsTheMissingStanding(t *testing.T) {
	f := newRebuildFixture(t, "header-back-renewal")
	ctx := context.Background()
	f.loseHeader(t)
	other := redis.NewClient(&redis.Options{Addr: f.client.Options().Addr})
	defer other.Close()
	f.client.AddHook(changeBeforeHeaderRebuild{client: other, key: f.prefix + ":activation_header", value: f.header})
	if outcome, err := f.repository.RebuildActivationHeader(ctx); err != nil || outcome != controlplane.ActivationHeaderRebuildConflict {
		t.Fatalf("rebuild = %s %v, want the other writer's header first", outcome, err)
	}
	if !f.repository.ActivationHeaderReading().Missing {
		t.Fatal("after the conflict the standing should still say missing")
	}
	if err := f.repository.RenewCurrentActivationObjects(ctx); err != nil {
		t.Fatalf("renewal with the header back: %v", err)
	}
	if f.repository.ActivationHeaderReading().Missing {
		t.Fatal("a renewal found the header and the standing still says missing")
	}
}
