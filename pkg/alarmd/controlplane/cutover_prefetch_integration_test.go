// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// timelineReadHook counts timeline GETs sent one at a time apart from the
// ones sent in a pipeline.
type timelineReadHook struct {
	mu                sync.Mutex
	single, pipelined int
}

func isTimelineGet(cmd redis.Cmder) bool {
	return strings.EqualFold(cmd.Name(), "get") && len(cmd.Args()) > 1 &&
		strings.Contains(fmt.Sprint(cmd.Args()[1]), ":schedule_timeline:")
}

func (hook *timelineReadHook) BeforeProcess(ctx context.Context, cmd redis.Cmder) (context.Context, error) {
	if isTimelineGet(cmd) {
		hook.mu.Lock()
		hook.single++
		hook.mu.Unlock()
	}
	return ctx, nil
}
func (*timelineReadHook) AfterProcess(context.Context, redis.Cmder) error { return nil }
func (hook *timelineReadHook) BeforeProcessPipeline(ctx context.Context, cmds []redis.Cmder) (context.Context, error) {
	for _, cmd := range cmds {
		if isTimelineGet(cmd) {
			hook.mu.Lock()
			hook.pipelined++
			hook.mu.Unlock()
		}
	}
	return ctx, nil
}
func (*timelineReadHook) AfterProcessPipeline(context.Context, []redis.Cmder) error { return nil }

// A process's first cutover reads every open Segment to check them against
// the manifest. It fetches them together, before the walk, in pipelined
// batches - not one round trip each, which on a few thousand Query Groups
// was a cutover of tens of seconds (N15 R2b-1). The outcome is the one the
// walk always gave.
func TestAFirstCutoverFetchesItsTimelinesTogether(t *testing.T) {
	fixture := newCutoverFixture(t, "alarmd:control:cutover-prefetch")
	first := cutoverCatalog(t, 80, nil)
	second := cutoverCatalog(t, 90, nil)
	edited, _ := splitEdited(t, first, second)
	fixture.publish(t, first, 60)

	// A fresh process: it has verified no open Segment yet.
	repository, err := controlplane.NewRedisCatalogRepository(fixture.client, fixture.prefix, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	compiler, semantics := runtimePlanCompiler(t)
	now := time.Unix(120, 0)
	reconciler, err := controlplane.NewScheduleActivationReconcilerWithProgress(repository, compiler, semantics,
		&activationProgressReader{byGroup: map[execution.QueryGroupIdentity]execution.ProgressLoadResult{}}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	snapshot, _, err := repository.PublishCatalog(fixture.ctx, second)
	if err != nil {
		t.Fatal(err)
	}
	hook := &timelineReadHook{}
	fixture.client.AddHook(hook)
	if _, err := reconciler.Ensure(fixture.ctx, snapshot.Publication); err != nil {
		t.Fatal(err)
	}
	if hook.single != 0 || hook.pipelined < len(first.QueryGroups) {
		t.Fatalf("the first cutover read timelines one at a time %d times and pipelined %d, want 0 and at least %d",
			hook.single, hook.pipelined, len(first.QueryGroups))
	}
	if cut := fixture.openSegment(t, edited.Identity, 120); cut.Start != 120 {
		t.Fatalf("the edited Query Group was not cut: %+v", cut)
	}
}
