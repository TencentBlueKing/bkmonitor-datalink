// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package strategy

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// A clear empties the cache and keeps the budget it was built with: the
// derived bounds, not a default, and the counters it had.
func TestForgetCachedEmptiesTheCacheAndKeepsItsBudget(t *testing.T) {
	compiler := newTestCompiler(t)
	request := validRequest(validPlan())
	if _, err := compiler.Compile(context.Background(), request); err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	before := compiler.CacheStats()
	if before.Entries != 1 || before.MaxEntries <= 0 || before.MaxBytes <= 0 {
		t.Fatalf("CacheStats() before = %+v", before)
	}
	compiler.ForgetCached()
	after := compiler.CacheStats()
	if after.Entries != 0 || after.Bytes != 0 || after.Clears != 1 {
		t.Fatalf("CacheStats() after = %+v, want empty with one clear", after)
	}
	if after.MaxEntries != before.MaxEntries || after.MaxBytes != before.MaxBytes || after.Misses != before.Misses {
		t.Fatalf("budget or counters changed across the clear: before %+v, after %+v", before, after)
	}
	if _, err := compiler.Compile(context.Background(), request); err != nil {
		t.Fatalf("Compile() after the clear error = %v", err)
	}
	if stats := compiler.CacheStats(); stats.Entries != 1 || stats.Misses != before.Misses+1 {
		t.Fatalf("CacheStats() after refilling = %+v, want the result compiled again and kept", stats)
	}
}

// Compiles running across clears neither panic nor return anything but the
// compiled result: a clear drops a memo, and a Compile that loses its entry
// to one compiles again. Run with -race.
func TestCompilesRunningAcrossAClearStillCompile(t *testing.T) {
	compiler := newTestCompiler(t)
	request := validRequest(validPlan())
	if result, err := compiler.Compile(context.Background(), request); err != nil {
		t.Fatalf("Compile() error = %v", err)
	} else if _, ok := result.Plan(); !ok {
		t.Fatalf("Compile() = %+v, want a compiled Plan", result)
	}
	var group sync.WaitGroup
	stop := make(chan struct{})
	errs := make(chan error, 8)
	for range 4 {
		group.Add(1)
		go func() {
			defer group.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				got, err := compiler.Compile(context.Background(), request)
				if err != nil {
					errs <- err
					return
				}
				if _, ok := got.Plan(); !ok || got.PlanTerminal() != nil {
					errs <- errors.New("a Compile across a clear returned no compiled Plan")
					return
				}
			}
		}()
	}
	for range 200 {
		compiler.ForgetCached()
	}
	close(stop)
	group.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("Compile() across clears: %v", err)
	}
	if stats := compiler.CacheStats(); stats.Clears != 200 || stats.Entries > 1 {
		t.Fatalf("CacheStats() = %+v, want 200 clears and at most the one entry", stats)
	}
}
