// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cleaner

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	redis "github.com/redis/go-redis/v9"
)

type backpressureTestRedis struct {
	mu      sync.Mutex
	groups  []redis.XInfoGroup
	err     error
	calls   int
	started chan struct{}
	release chan struct{}
}

type backpressureTestRedisError string

func (e backpressureTestRedisError) Error() string { return string(e) }

func (backpressureTestRedisError) RedisError() {}

func (f *backpressureTestRedis) XInfoGroups(ctx context.Context, _ string) *redis.XInfoGroupsCmd {
	f.mu.Lock()
	f.calls++
	groups := append([]redis.XInfoGroup(nil), f.groups...)
	err := f.err
	started, release := f.started, f.release
	f.mu.Unlock()
	if started != nil {
		select {
		case started <- struct{}{}:
		default:
		}
	}
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			err = ctx.Err()
		}
	}
	cmd := redis.NewXInfoGroupsCmd(ctx, "stream")
	cmd.SetVal(groups)
	cmd.SetErr(err)
	return cmd
}

func (f *backpressureTestRedis) set(groups []redis.XInfoGroup, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.groups = append([]redis.XInfoGroup(nil), groups...)
	f.err = err
}

func (f *backpressureTestRedis) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

type backpressureTestObserver struct {
	checks      []BackpressureObservation
	transitions []string
}

func (o *backpressureTestObserver) BackpressureChecked(_ context.Context, observation BackpressureObservation) {
	o.checks = append(o.checks, observation)
}

func (o *backpressureTestObserver) BackpressureTransition(_ context.Context, action string) {
	o.transitions = append(o.transitions, action)
}

func TestSignalBackpressureCheckerCachesAndAppliesHysteresis(t *testing.T) {
	client := &backpressureTestRedis{groups: []redis.XInfoGroup{{Name: "lifecycle", Lag: 100000}}}
	observer := &backpressureTestObserver{}
	now := time.Unix(100, 0)
	checker, err := newSignalBackpressureChecker(client, backpressureTestConfig(), observer, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}

	if decision := checker.Check(context.Background()); decision.Allowed {
		t.Fatalf("high watermark decision=%+v", decision)
	}
	if decision := checker.Check(context.Background()); decision.Allowed || client.callCount() != 1 {
		t.Fatalf("cached decision=%+v calls=%d", decision, client.callCount())
	}

	now = now.Add(3 * time.Second)
	client.set([]redis.XInfoGroup{{Name: "lifecycle", Lag: 90000}}, nil)
	if decision := checker.Check(context.Background()); decision.Allowed {
		t.Fatalf("hysteresis decision=%+v", decision)
	}

	now = now.Add(3 * time.Second)
	client.set([]redis.XInfoGroup{{Name: "lifecycle", Lag: 79999, Pending: 1}}, nil)
	if decision := checker.Check(context.Background()); !decision.Allowed {
		t.Fatalf("low watermark decision=%+v", decision)
	}
	if got := observer.transitions; len(got) != 2 || got[0] != "pause" || got[1] != "resume" {
		t.Fatalf("transitions=%v", got)
	}
}

func TestSignalBackpressureCheckerFailureModes(t *testing.T) {
	tests := []struct {
		name        string
		groups      []redis.XInfoGroup
		err         error
		wantAllowed bool
		wantOutcome string
	}{
		{name: "query failure is open", err: errors.New("redis unavailable"), wantAllowed: true, wantOutcome: "query_failed"},
		{name: "missing stream is closed", err: backpressureTestRedisError("ERR no such key"), wantAllowed: false, wantOutcome: "group_missing"},
		{name: "unknown lag is open", groups: []redis.XInfoGroup{{Name: "lifecycle", Lag: -1, Pending: 10}}, wantAllowed: true, wantOutcome: "lag_unknown"},
		{name: "missing group is closed", groups: []redis.XInfoGroup{{Name: "other", Lag: 0}}, wantAllowed: false, wantOutcome: "group_missing"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &backpressureTestRedis{groups: test.groups, err: test.err}
			observer := &backpressureTestObserver{}
			checker, err := newSignalBackpressureChecker(client, backpressureTestConfig(), observer, time.Now)
			if err != nil {
				t.Fatal(err)
			}
			if decision := checker.Check(context.Background()); decision.Allowed != test.wantAllowed {
				t.Fatalf("decision=%+v", decision)
			}
			if len(observer.checks) != 1 || observer.checks[0].Outcome != test.wantOutcome {
				t.Fatalf("checks=%+v", observer.checks)
			}
		})
	}
}

func TestSignalBackpressureCheckerAllowsConcurrentCallersToUseCache(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	client := &backpressureTestRedis{
		groups: []redis.XInfoGroup{{Name: "lifecycle", Lag: 0}}, started: started, release: release,
	}
	checker, err := newSignalBackpressureChecker(client, backpressureTestConfig(), nil, time.Now)
	if err != nil {
		t.Fatal(err)
	}

	firstDone := make(chan struct{})
	go func() {
		checker.Check(context.Background())
		close(firstDone)
	}()
	<-started

	var allowed atomic.Int64
	var callers sync.WaitGroup
	for range 20 {
		callers.Add(1)
		go func() {
			defer callers.Done()
			if checker.Check(context.Background()).Allowed {
				allowed.Add(1)
			}
		}()
	}
	callers.Wait()
	if allowed.Load() != 20 || client.callCount() != 1 {
		t.Fatalf("allowed=%d calls=%d", allowed.Load(), client.callCount())
	}
	close(release)
	<-firstDone
}

func backpressureTestConfig() BackpressureConfig {
	return BackpressureConfig{
		Stream: "signals", Group: "lifecycle", CacheTTL: 3 * time.Second,
		QueryTimeout: time.Second, HighWatermark: 100000, LowWatermark: 80000,
	}
}

var _ backpressureRedis = (*backpressureTestRedis)(nil)
