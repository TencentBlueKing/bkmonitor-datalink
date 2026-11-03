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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

// faultySource fails one call of the source on demand, and can answer the
// active set differently on consecutive calls, which is what an unstable
// observation looks like from the reconciler.
type faultySource struct {
	inner            *countingStrategySource
	signalErr        error
	activeSetErr     error
	documentsErr     error
	alternateActives [][]string
	activeCalls      int
}

func (source *faultySource) ActiveStrategyIDs(ctx context.Context) ([]string, error) {
	source.activeCalls++
	if source.activeSetErr != nil {
		return nil, source.activeSetErr
	}
	if len(source.alternateActives) > 0 {
		return append([]string(nil), source.alternateActives[(source.activeCalls-1)%len(source.alternateActives)]...), nil
	}
	return source.inner.ActiveStrategyIDs(ctx)
}

func (source *faultySource) Strategies(ctx context.Context, ids []string) ([]controlplane.SourceStrategy, error) {
	if source.documentsErr != nil {
		return nil, source.documentsErr
	}
	return source.inner.Strategies(ctx, ids)
}

func (source *faultySource) ChangeSignal(ctx context.Context) (controlplane.SourceChangeSignal, error) {
	if source.signalErr != nil {
		return controlplane.SourceChangeSignal{}, source.signalErr
	}
	return source.inner.ChangeSignal(ctx)
}

// setActiveSet and deleteActiveSet edit the published active set the way a
// publisher that writes something wrong would, without touching the change
// signal.
func (harness *changeGateHarness) setActiveSet(payload string) {
	harness.t.Helper()
	if err := harness.client.Set(harness.ctx, "bkmonitor.cache.strategy_ids", payload, 0).Err(); err != nil {
		harness.t.Fatal(err)
	}
}

func (harness *changeGateHarness) deleteActiveSet() {
	harness.t.Helper()
	if err := harness.client.Del(harness.ctx, "bkmonitor.cache.strategy_ids").Err(); err != nil {
		harness.t.Fatal(err)
	}
}

// A round that fails used to return an error and nothing else, and the
// runtime that kept the last good catalog counted the first failure of an
// episode and no other. Every failing return now names its exit, the source
// adapters name theirs, and the two "one bad element refuses the whole set"
// exits are told apart from the store being unreachable: they are fixed by
// different people. The cause keeps its identity and its text through the
// wrapper, so nothing that already matched on either is affected.
func TestSourceRefreshExitsNameWhereARoundStopped(t *testing.T) {
	harness := newChangeGateHarness(t)
	harness.settle()
	storeDown := errors.New("store: connection refused")

	arms := []struct {
		name    string
		arrange func(*faultySource)
		exit    controlplane.SourceRefreshExit
		is      error
		text    string
	}{
		{
			name: "one element that is not a canonical integer refuses the set",
			arrange: func(*faultySource) {
				harness.setActiveSet(`[1001, "1002"]`)
			},
			exit: controlplane.SourceRefreshExitActiveSetInvalidID,
			is:   controlplane.ErrLegacySourceIncomplete,
			text: `element 1 of 2 is "\"1002\""`,
		},
		{
			name: "an identity listed twice refuses the set",
			arrange: func(*faultySource) {
				harness.setActiveSet(`[1001, 1002, 1001]`)
			},
			exit: controlplane.SourceRefreshExitActiveSetDuplicate,
			is:   controlplane.ErrActiveSetNotCanonical,
			text: `element "1001" at 1 of 3`,
		},
		{
			name: "an absent set",
			arrange: func(*faultySource) {
				harness.deleteActiveSet()
			},
			exit: controlplane.SourceRefreshExitActiveSetMissing,
			is:   controlplane.ErrLegacySourceIncomplete,
		},
		{
			name:    "the store refusing the set read",
			arrange: func(source *faultySource) { source.activeSetErr = storeDown },
			exit:    controlplane.SourceRefreshExitActiveSetRead,
			is:      storeDown,
		},
		{
			name:    "the change signal read failing",
			arrange: func(source *faultySource) { source.signalErr = storeDown },
			exit:    controlplane.SourceRefreshExitChangeSignal,
			is:      storeDown,
		},
		{
			name:    "the document read failing",
			arrange: func(source *faultySource) { source.documentsErr = storeDown },
			exit:    controlplane.SourceRefreshExitDocuments,
			is:      storeDown,
		},
		{
			name: "the set changing under the read",
			arrange: func(source *faultySource) {
				source.alternateActives = [][]string{{"1001", "1002"}, {"1001"}}
			},
			exit: controlplane.SourceRefreshExitObservationUnstable,
			is:   controlplane.ErrObservationUnstable,
		},
	}
	for _, arm := range arms {
		t.Run(arm.name, func(t *testing.T) {
			// A settled reconciler skips the read while nothing signals a
			// change; moving the signal is what makes it read again.
			harness.clock = harness.clock.Add(time.Minute)
			harness.signal(harness.clock.Add(-time.Second))
			source := &faultySource{inner: harness.source}
			arm.arrange(source)
			_, err := harness.reconciler.Refresh(harness.ctx, source, harness.planner)
			if err == nil {
				t.Fatal("Refresh() succeeded; the arm arranged nothing that fails")
			}
			if exit := controlplane.SourceRefreshExitOf(err); exit != arm.exit {
				t.Fatalf("exit = %s, want %s (%v)", exit, arm.exit, err)
			}
			if !errors.Is(err, arm.is) {
				t.Fatalf("the cause lost its identity through the exit: %v", err)
			}
			if arm.text != "" && !strings.Contains(err.Error(), arm.text) {
				t.Fatalf("error text %q does not name the element, want %q", err.Error(), arm.text)
			}
			var failure *controlplane.SourceRefreshFailure
			if !errors.As(err, &failure) || failure.Error() != failure.Err.Error() {
				t.Fatalf("the wrapper changed the error text: %v", err)
			}
			// The other direction: the source restored, the next round
			// succeeds and reads as no exit.
			harness.setActiveSet(`[1001, 1002]`)
			harness.clock = harness.clock.Add(time.Minute)
			harness.signal(harness.clock.Add(-time.Second))
			result, err := harness.reconciler.Refresh(harness.ctx, harness.source, harness.planner)
			if err != nil || controlplane.SourceRefreshExitOf(err) != controlplane.SourceRefreshExitNone {
				t.Fatalf("the round after the fault = (%+v, %v), want success", result, err)
			}
		})
	}
}

// An error nothing claimed reads as other rather than as a made-up exit,
// and a nil error as none.
func TestSourceRefreshExitOfAnUnclaimedError(t *testing.T) {
	if exit := controlplane.SourceRefreshExitOf(errors.New("unclaimed")); exit != controlplane.SourceRefreshExitOther {
		t.Fatalf("unclaimed error exit = %s, want other", exit)
	}
	if exit := controlplane.SourceRefreshExitOf(nil); exit != controlplane.SourceRefreshExitNone {
		t.Fatalf("nil error exit = %s, want none", exit)
	}
	for _, exit := range controlplane.SourceRefreshExits {
		if !controlplane.ValidSourceRefreshExit(exit) {
			t.Fatalf("%s is listed but not valid", exit)
		}
	}
	if controlplane.ValidSourceRefreshExit("made_up") {
		t.Fatal("an exit outside the closed set is valid")
	}
}

// The time of the last successful round is a persisted fact: read back after
// the process that wrote it is gone, and absent, not zero, where no round
// ever succeeded.
func TestSourceRefreshSuccessMarkIsPersisted(t *testing.T) {
	ctx := context.Background()
	client := newControlplaneRedis(t)
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:success-mark", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if at, known, err := repository.LoadSourceRefreshSuccess(ctx); err != nil || known || !at.IsZero() {
		t.Fatalf("before any success: (%v, %v, %v), want unknown", at, known, err)
	}
	marked := time.Unix(1_700_000_000, 0)
	if err := repository.MarkSourceRefreshSuccess(ctx, marked); err != nil {
		t.Fatal(err)
	}
	// Another repository over the same prefix stands in for the process that
	// starts after this one is gone.
	restarted, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:success-mark", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	at, known, err := restarted.LoadSourceRefreshSuccess(ctx)
	if err != nil || !known || !at.Equal(marked) {
		t.Fatalf("after a restart: (%v, %v, %v), want %v", at, known, err, marked)
	}
	if ttl := client.TTL(ctx, "alarmd:control:success-mark:source_refreshed_at").Val(); ttl != -1*time.Nanosecond && ttl > 0 {
		t.Fatalf("the mark expires in %v; it must not expire, an expiry makes it absent exactly when it is largest", ttl)
	}
	if err := repository.MarkSourceRefreshSuccess(ctx, time.Time{}); err == nil {
		t.Fatal("a zero time was accepted as a success time")
	}
}
