// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The bound has to come from the derivation, not from the test. Every existing
// fanout assertion sets ActiveExecutionLimit by hand first, so all of them
// would keep passing on a build that derived nothing at all - which is exactly
// the build that ran in production and reached 452 outstanding invocations
// against a 32-permit gate.
//
// So this one reads the limit out of the configuration the process would run
// under and gives the dispatcher more Query Groups than that. Every Runner
// blocks on entry, so an unbounded dispatcher admits all of them and a bounded
// one stops at its limit; nothing here depends on timing to tell those apart.
func TestDispatcherFanoutComesFromTheDerivedLimit(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	limit := cfg.PhaseTwo.Scheduler.ActiveExecutionLimit
	if limit <= 0 {
		t.Fatalf("derived active execution limit = %d, want a bound the dispatcher can hold", limit)
	}

	var mu sync.Mutex
	active, peak := 0, 0
	release := make(chan struct{})
	observed := make(chan int, 1)
	enter := func() func() {
		mu.Lock()
		active++
		if active > peak {
			peak = active
		}
		reached := active
		mu.Unlock()
		if reached >= limit {
			select {
			case observed <- reached:
			default:
			}
		}
		<-release
		return func() {
			mu.Lock()
			active--
			mu.Unlock()
		}
	}

	// More Query Groups than slots, so a dispatcher that honours the bound has
	// to leave some of them queued.
	total := limit + limit/2 + 1
	bundle := &phaseTwoWorkerBundle{
		dependencies: phaseTwoWorkerBundleDependencies{
			Config: cfg, Now: time.Now, Observer: observability.NopObserver{},
		},
		runners: make(map[execution.QueryGroupIdentity]*phaseTwoQueryGroupLifecycle, total),
	}
	for index := range total {
		queryGroup := execution.QueryGroupIdentity(fmt.Sprintf("query-group-%04d", index))
		bundle.runners[queryGroup] = &phaseTwoQueryGroupLifecycle{runner: &callbackPhaseTwoQueryGroup{
			run: func(context.Context) (execution.SlotExecutionResult, bool, error) {
				defer enter()()
				return execution.SlotExecutionResult{Completed: true, Result: observability.ResultSuccess}, true, nil
			},
		}}
	}

	done := make(chan error, 1)
	go func() { done <- bundle.runScheduledOnce(context.Background()) }()

	select {
	case <-observed:
	case <-time.After(10 * time.Second):
		mu.Lock()
		reached := active
		mu.Unlock()
		close(release)
		<-done
		t.Fatalf("dispatcher reached %d concurrent Runners, want the derived %d", reached, limit)
	}
	// Every slot is occupied and every remaining Query Group is queued behind
	// one. Give the dispatcher room to overshoot if it is going to, then read
	// the peak once the run has drained.
	time.Sleep(100 * time.Millisecond)
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("runScheduledOnce() error = %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if peak > limit {
		t.Fatalf("dispatcher fanout peaked at %d with %d Query Groups owned, want at most the derived %d",
			peak, total, limit)
	}
	if peak < limit {
		t.Fatalf("dispatcher fanout peaked at %d, want it to use the whole derived %d", peak, limit)
	}
}
