// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane_test

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

// A publication writes the activation as a head: the records are on the open
// Segments, and the body every replica rereads after the header moves is a
// few hundred bytes whatever the population (N15). These pin what that
// changes and what it must not.

// evalArgsHook keeps the arguments of every script call, so a test can say
// what a publication sent.
type evalArgsHook struct {
	mu   sync.Mutex
	args [][]string
}

func (hook *evalArgsHook) BeforeProcess(ctx context.Context, cmd redis.Cmder) (context.Context, error) {
	if strings.EqualFold(cmd.Name(), "eval") || strings.EqualFold(cmd.Name(), "evalsha") {
		values := make([]string, 0, len(cmd.Args()))
		for _, arg := range cmd.Args() {
			if raw, ok := arg.([]byte); ok {
				values = append(values, string(raw))
				continue
			}
			values = append(values, fmt.Sprint(arg))
		}
		hook.mu.Lock()
		hook.args = append(hook.args, values)
		hook.mu.Unlock()
	}
	return ctx, nil
}
func (*evalArgsHook) AfterProcess(context.Context, redis.Cmder) error { return nil }
func (*evalArgsHook) BeforeProcessPipeline(ctx context.Context, _ []redis.Cmder) (context.Context, error) {
	return ctx, nil
}
func (*evalArgsHook) AfterProcessPipeline(context.Context, []redis.Cmder) error { return nil }

func (hook *evalArgsHook) sent(value string) bool {
	hook.mu.Lock()
	defer hook.mu.Unlock()
	for _, call := range hook.args {
		for _, arg := range call {
			if arg == value {
				return true
			}
		}
	}
	return false
}

// The body is a head. The leader that wrote it reads its records back from
// its own write without reading a timeline; a leader that did not write it
// reads them from the open Segments, and gets the same records.
func TestThePublicationWritesAHeadAndTheLeaderReadsItsOwnWriteBack(t *testing.T) {
	fixture := newControlReadCacheFixture(t, "head-writer")
	ctx := context.Background()
	raw, err := controlplane.ActivationBytesForTest(ctx, fixture.repository)
	if err != nil || !strings.Contains(string(raw), `"alarmd-control-activation-v3"`) || strings.Contains(string(raw), `"fact"`) {
		t.Fatalf("body = %s (%v), want a head without records", raw, err)
	}

	fixture.hook.reset()
	written, err := fixture.repository.LoadActivation(ctx)
	if err != nil || len(written.Plans) == 0 {
		t.Fatalf("the leader's own activation = (%+v, %v), want its records", written, err)
	}
	if reads := fixture.hook.bodyReads("timeline"); reads != 0 {
		t.Fatalf("the leader read %d timelines to get back what it wrote, want 0", reads)
	}

	cold, _ := fixture.coldRepository(t)
	fixture.hook.reset()
	read, err := cold.LoadActivation(ctx)
	if err != nil || !reflect.DeepEqual(sortedRecords(read.Plans), sortedRecords(written.Plans)) {
		t.Fatalf("a leader that did not write it read (%+v, %v), want the records written %+v", read.Plans, err, written.Plans)
	}
	if reads := fixture.hook.bodyReads("timeline"); reads == 0 {
		t.Fatal("a leader that did not write the head got records without reading the open Segments")
	}
}

// A publication that leaves the active set as it was does not write it
// again, and no publication hands the set to the cutover script: the script
// checks the key named by its digest. A replica reads each set once, and a
// set that is gone still reads as gone.
func TestTheActiveSetIsCheckedByReferenceAndReadOncePerDigest(t *testing.T) {
	fixture := newControlReadCacheFixture(t, "active-set-reference")
	ctx := context.Background()
	state, err := fixture.repository.LoadActivationHead(ctx)
	if err != nil {
		t.Fatal(err)
	}
	setKey := fixture.prefix + ":active_qg_set:" + state.ActiveQGSetRef.Digest
	setPayload, err := fixture.client.Get(ctx, setKey).Result()
	if err != nil {
		t.Fatal(err)
	}
	hook := &evalArgsHook{}
	fixture.client.AddHook(hook)
	fixture.hook.reset()

	*fixture.clock = time.Unix(180, 0)
	second, _, err := fixture.repository.PublishCatalog(ctx, catalogWithSchedule(t, validCatalog(t, 81), 60, 0))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.reconciler.Ensure(ctx, second.Publication); err != nil {
		t.Fatal(err)
	}
	after, err := fixture.repository.LoadActivationHead(ctx)
	if err != nil || after.ActiveQGSetRef != state.ActiveQGSetRef || after.Current != second.Publication {
		t.Fatalf("after the publication = (%+v, %v), want the same set under the new publication", after, err)
	}
	// go-redis sends SetNX with an expiry as SET ... NX.
	if writes := fixture.hook.count("set", "active_set") + fixture.hook.count("setnx", "active_set"); writes != 0 {
		t.Fatalf("an unchanged set was written %d times, want 0", writes)
	}
	if hook.sent(setPayload) {
		t.Fatal("the cutover script was handed the whole active set")
	}

	cold, _ := fixture.coldRepository(t)
	fixture.hook.reset()
	for range 3 {
		groups, err := cold.LoadActiveQueryGroupSet(ctx, after.ActiveQGSetRef)
		if err != nil || len(groups) != int(after.ActiveQGSetRef.QGCount) {
			t.Fatalf("active set = (%v, %v)", groups, err)
		}
	}
	if reads := fixture.hook.count("get", "active_set"); reads != 1 {
		t.Fatalf("the set was read %d times for three reads of one digest, want 1", reads)
	}
	if err := fixture.client.Del(ctx, setKey).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := cold.LoadActiveQueryGroupSet(ctx, after.ActiveQGSetRef); err != controlplane.ErrSnapshotUnavailable {
		t.Fatalf("a set that is gone read as %v, want ErrSnapshotUnavailable", err)
	}
}

// A timeline rewritten under an unchanged header - the repair subcommand
// does that - is what a head's records are read back from, not a copy the
// timeline cache took before the rewrite: those records decide the next
// activation.
func TestAHeadIsReadBackFromTheTimelineAsItIsNotAsCached(t *testing.T) {
	fixture := newControlReadCacheFixture(t, "head-live-read")
	ctx := context.Background()
	cold, runtime := fixture.coldRepository(t)
	// Warm the cold repository's timeline cache under the current header.
	if _, err := runtime.ReadFrozenSchedule(ctx, fixture.queryGroup, 120); err != nil {
		t.Fatal(err)
	}
	key := fixture.prefix + ":schedule_timeline:" + string(fixture.queryGroup)
	raw, err := fixture.client.Get(ctx, key).Result()
	if err != nil {
		t.Fatal(err)
	}
	rewritten := strings.Replace(raw, `"RequiredFullSlots":`, `"RequiredFullSlots":7`, 1)
	if rewritten == raw {
		t.Fatal("setup: no record to rewrite")
	}
	if err := fixture.client.Set(ctx, key, rewritten, time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	state, err := cold.LoadActivation(ctx)
	if err != nil || len(state.Plans) == 0 {
		t.Fatalf("activation = (%+v, %v)", state, err)
	}
	if got := state.Plans[0].Fact.Selected.RequiredFullSlots; got < 70 {
		t.Fatalf("records read back with RequiredFullSlots=%d, want the rewritten timeline's", got)
	}
}
