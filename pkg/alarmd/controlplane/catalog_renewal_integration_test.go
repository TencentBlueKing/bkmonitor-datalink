// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane_test

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/redisfailure"
)

// renewalCallers is the job each PEXPIRE the client sends names, by the kind
// of catalog key it renews.
type renewalCallers struct {
	mu     sync.Mutex
	prefix string
	byKind map[string][]string
}

func (hook *renewalCallers) note(ctx context.Context, cmd redis.Cmder) {
	if cmd.Name() != "pexpire" || len(cmd.Args()) < 2 {
		return
	}
	key := fmt.Sprint(cmd.Args()[1])
	kind := strings.SplitN(strings.TrimPrefix(key, hook.prefix+":"), ":", 2)[0]
	hook.mu.Lock()
	defer hook.mu.Unlock()
	hook.byKind[kind] = append(hook.byKind[kind], redisfailure.Caller(ctx))
}

func (hook *renewalCallers) take() map[string][]string {
	hook.mu.Lock()
	defer hook.mu.Unlock()
	taken := hook.byKind
	hook.byKind = map[string][]string{}
	return taken
}

func (hook *renewalCallers) BeforeProcess(ctx context.Context, cmd redis.Cmder) (context.Context, error) {
	hook.note(ctx, cmd)
	return ctx, nil
}

func (hook *renewalCallers) AfterProcess(context.Context, redis.Cmder) error { return nil }

func (hook *renewalCallers) BeforeProcessPipeline(ctx context.Context, cmds []redis.Cmder) (context.Context, error) {
	for _, cmd := range cmds {
		hook.note(ctx, cmd)
	}
	return ctx, nil
}

func (hook *renewalCallers) AfterProcessPipeline(context.Context, []redis.Cmder) error { return nil }

type activeSetRenewals struct {
	mu    sync.Mutex
	facts []observability.ActiveQGSetFacts
}

func (observer *activeSetRenewals) Observe(_ context.Context, observation observability.Observation) {
	if observation.ActiveQGSet == nil || observation.ActiveQGSet.Operation != "renew" {
		return
	}
	observer.mu.Lock()
	defer observer.mu.Unlock()
	observer.facts = append(observer.facts, *observation.ActiveQGSet)
}

func (observer *activeSetRenewals) last(t *testing.T) observability.ActiveQGSetFacts {
	t.Helper()
	observer.mu.Lock()
	defer observer.mu.Unlock()
	if len(observer.facts) == 0 {
		t.Fatal("no renewal was observed")
	}
	return observer.facts[len(observer.facts)-1]
}

// A process renews every catalog key the current activation references on
// its first refresh round, each kind under its own job, and counts the
// timelines as ones its term had not renewed. Its next round renews no
// timeline, object or context; once a third of the catalog TTL has passed it
// renews the timelines, counted as aged, and not the objects, which wait for
// the tenth round. A new term starts over: every timeline as first, and the
// objects and contexts; the same term told again does not.
func TestEachCatalogRenewalNamesItsJobAndRenewsOnItsOwnCadence(t *testing.T) {
	harness := newObjectCatalogHarness(t)
	catalog := objectCatalogTwoGroups(t, 80)
	snapshot := harness.publish(t, catalog)
	progress := &activationProgressReader{byGroup: map[execution.QueryGroupIdentity]execution.ProgressLoadResult{}}
	compiler, semantics := runtimePlanCompiler(t)
	reconciler, err := controlplane.NewScheduleActivationReconcilerWithProgress(harness.repository, compiler, semantics, progress,
		func() time.Time { return time.Unix(60, 0) })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Ensure(harness.ctx, snapshot.Publication); err != nil {
		t.Fatal(err)
	}

	callers := &renewalCallers{prefix: harness.prefix, byKind: map[string][]string{}}
	harness.client.AddHook(callers)
	// The process that activated the publication wrote both timelines with
	// the full TTL: its first round renews the objects and not them.
	if err := harness.repository.RenewCurrentActivationObjects(harness.ctx); err != nil {
		t.Fatal(err)
	}
	if named := callers.take(); len(named["schedule_timeline"]) != 0 || len(named["qgobj"]) != 2 {
		t.Fatalf("the activating process's first round renewed %v, want the objects and no timeline", named)
	}
	const catalogTTL = time.Hour
	fresh, err := controlplane.NewRedisCatalogRepository(harness.client, harness.prefix, catalogTTL)
	if err != nil {
		t.Fatal(err)
	}
	renewals := &activeSetRenewals{}
	fresh.ConfigureObserver(renewals)
	renewedAt := time.Unix(1_700_000_000, 0)
	controlplane.SetCatalogRenewalClockForTest(fresh, func() time.Time { return renewedAt })

	if err := fresh.RenewCurrentActivationObjects(harness.ctx); err != nil {
		t.Fatal(err)
	}
	named := callers.take()
	for kind, want := range map[string]struct {
		caller string
		keys   int
	}{
		"schedule_timeline": {redisfailure.CallerCatalogTimelineRenewal, 2},
		"qgobj":             {redisfailure.CallerCatalogObjectRenewal, 2},
		"manifest":          {redisfailure.CallerCatalogObjectRenewal, 1},
		"outctx":            {redisfailure.CallerCatalogContextRenewal, 2},
	} {
		if got := named[kind]; len(got) != want.keys || slices.ContainsFunc(got, func(caller string) bool { return caller != want.caller }) {
			t.Fatalf("the first round renewed %s keys under %q, want %d under %q", kind, got, want.keys, want.caller)
		}
	}
	if facts := renewals.last(t); facts.Result != "success" || facts.TimelinesFirst != 2 || facts.TimelinesAged != 0 {
		t.Fatalf("first round facts=%+v, want two timelines counted first", facts)
	}

	renewedAt = renewedAt.Add(catalogTTL/3 - time.Second)
	if err := fresh.RenewCurrentActivationObjects(harness.ctx); err != nil {
		t.Fatal(err)
	}
	if named := callers.take(); len(named) != 0 {
		t.Fatalf("the second round, inside a third of the TTL, renewed %v", named)
	}
	if facts := renewals.last(t); facts.Result != "success" || facts.TimelinesFirst != 0 || facts.TimelinesAged != 0 {
		t.Fatalf("second round facts=%+v, want no timeline", facts)
	}

	renewedAt = renewedAt.Add(time.Second)
	if err := fresh.RenewCurrentActivationObjects(harness.ctx); err != nil {
		t.Fatal(err)
	}
	if named, want := callers.take(), map[string][]string{"schedule_timeline": {redisfailure.CallerCatalogTimelineRenewal, redisfailure.CallerCatalogTimelineRenewal}}; !reflect.DeepEqual(named, want) {
		t.Fatalf("the third round, at a third of the TTL, renewed %v, want %v", named, want)
	}
	if facts := renewals.last(t); facts.TimelinesFirst != 0 || facts.TimelinesAged != 2 {
		t.Fatalf("third round facts=%+v, want two timelines counted aged", facts)
	}

	fresh.BeginLeaderTerm(7)
	if err := fresh.RenewCurrentActivationObjects(harness.ctx); err != nil {
		t.Fatal(err)
	}
	if named := callers.take(); len(named["schedule_timeline"]) != 2 || len(named["qgobj"]) != 2 || len(named["outctx"]) != 2 {
		t.Fatalf("a new term's first round renewed %v, want every timeline, object and context", named)
	}
	if facts := renewals.last(t); facts.TimelinesFirst != 2 || facts.TimelinesAged != 0 {
		t.Fatalf("a new term's first round facts=%+v, want two timelines counted first", facts)
	}
	fresh.BeginLeaderTerm(7)
	if err := fresh.RenewCurrentActivationObjects(harness.ctx); err != nil {
		t.Fatal(err)
	}
	if named := callers.take(); len(named) != 0 {
		t.Fatalf("the same term told again renewed %v, want nothing", named)
	}
}
