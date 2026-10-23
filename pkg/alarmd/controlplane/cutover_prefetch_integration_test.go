// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
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
// the manifest. It reads them in pipelined batches a window ahead of the
// walk - not one round trip each, which on a few thousand Query Groups was
// a cutover of tens of seconds (N15 R2b-1). The outcome is the one the walk
// always gave.
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

// windowFailureHook fails the cutover walk's second window of timelines:
// the walk reads the timelines' lengths in one pipeline, then each window's
// values in one, and the second of those after the lengths fails with err.
// The reads of the open Segments before the walk read no lengths, and are
// left alone.
type windowFailureHook struct {
	mu          sync.Mutex
	err         error
	sized       bool
	windows     int
	failedAfter bool
}

func (*windowFailureHook) BeforeProcess(ctx context.Context, _ redis.Cmder) (context.Context, error) {
	return ctx, nil
}
func (*windowFailureHook) AfterProcess(context.Context, redis.Cmder) error { return nil }
func (hook *windowFailureHook) BeforeProcessPipeline(ctx context.Context, cmds []redis.Cmder) (context.Context, error) {
	hook.mu.Lock()
	defer hook.mu.Unlock()
	if len(cmds) == 0 || !strings.Contains(fmt.Sprint(cmds[0].Args()...), ":schedule_timeline:") {
		return ctx, nil
	}
	switch strings.ToLower(cmds[0].Name()) {
	case "strlen":
		hook.sized = true
	case "get":
		if hook.sized {
			hook.windows++
			if hook.windows == 2 {
				hook.failedAfter = true
				return ctx, hook.err
			}
		}
	}
	return ctx, nil
}
func (*windowFailureHook) AfterProcessPipeline(context.Context, []redis.Cmder) error { return nil }

// answeredError is Redis answering a command with an error.
type answeredError string

func (err answeredError) Error() string { return string(err) }
func (answeredError) RedisError()       {}

// A cutover whose walk fails part way - the second window of timelines
// fails at the transport, or Redis answers every key of it with an error
// as while it loads - fails as the dependency's and writes nothing: the
// activation and every timeline are as they were, the ones the walk had
// already decided on included.
func TestACutoverFailingPartWayThroughItsWalkWritesNothing(t *testing.T) {
	for _, failure := range []error{errors.New("i/o timeout"), answeredError("LOADING Redis is loading the dataset in memory")} {
		fixture := newCutoverFixture(t, "alarmd:control:cutover-part-way")
		first := cutoverCatalog(t, 80, nil)
		second := cutoverCatalog(t, 90, nil)
		fixture.publish(t, first, 60)
		before := fixture.activation(t)
		timelines := map[execution.QueryGroupIdentity][]byte{}
		for _, group := range first.QueryGroups {
			timelines[group.Identity] = fixture.timelineBytes(t, group.Identity)
		}

		// A fresh process whose read bound is below one timeline: each
		// window is one timeline, so the walk decides on the first before
		// the second fails.
		repository, err := controlplane.NewRedisCatalogRepository(fixture.client, fixture.prefix, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		if err := repository.ConfigureControlTimelineCache(1, 1); err != nil {
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
		hook := &windowFailureHook{err: failure}
		fixture.client.AddHook(hook)
		_, err = reconciler.Ensure(fixture.ctx, snapshot.Publication)
		var dependency *controlplane.ActivationDependencyIOError
		if !hook.failedAfter || !errors.As(err, &dependency) {
			t.Fatalf("%v: the second window failed=%v, the cutover returned %v; want the dependency's", failure, hook.failedAfter, err)
		}
		if after := fixture.activation(t); !reflect.DeepEqual(after, before) {
			t.Fatalf("%v: a cutover that failed part way wrote the activation", failure)
		}
		for identity, stored := range timelines {
			if got := fixture.timelineBytes(t, identity); !reflect.DeepEqual(got, stored) {
				t.Fatalf("%v: a cutover that failed part way wrote the timeline of %s", failure, identity)
			}
		}
	}
}
