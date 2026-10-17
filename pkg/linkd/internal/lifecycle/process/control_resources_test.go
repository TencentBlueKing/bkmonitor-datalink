// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lifecycleprocess

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/eventsource"
)

func TestControlHooksReuseAndRetireAfterLastOperation(t *testing.T) {
	closed := 0
	old := controlHookKey{"source", 1}
	r := &AlertProcessorResources{hooks: map[controlHookKey]*controlHooks{old: {close: func() error { closed++; return nil }}}, latest: map[string]controlHookKey{}}
	_, releaseOne, err := r.acquireHooks(eventsource.Release{ID: "source", Version: 1}, config.Config{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, releaseTwo, err := r.acquireHooks(eventsource.Release{ID: "source", Version: 1}, config.Config{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, releaseNew, err := r.acquireHooks(eventsource.Release{ID: "source", Version: 2}, config.Config{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if closed != 0 || len(r.hooks) != 2 {
		t.Fatalf("closed=%d entries=%d", closed, len(r.hooks))
	}
	if err := releaseOne(); err != nil {
		t.Fatal(err)
	}
	if closed != 0 {
		t.Fatal("closed hooks still in use")
	}
	if err := releaseTwo(); err != nil {
		t.Fatal(err)
	}
	if closed != 1 || len(r.hooks) != 1 {
		t.Fatalf("closed=%d entries=%d", closed, len(r.hooks))
	}
	if err := releaseNew(); err != nil {
		t.Fatal(err)
	}
	// 不可变 Release 已随 Hook 缓存；相同版本读取不再访问来源存储。
	cached, err := r.sourceRelease(t.Context(), nil, "source", 2)
	if err != nil || cached.ID != "source" || cached.Version != 2 {
		t.Fatalf("cached release=%+v error=%v", cached, err)
	}
}

func TestControlResourcesConcurrentHookUsers(t *testing.T) {
	r := &AlertProcessorResources{hooks: map[controlHookKey]*controlHooks{}, latest: map[string]controlHookKey{}}
	var group sync.WaitGroup
	for range 32 {
		group.Go(func() {
			_, release, err := r.acquireHooks(eventsource.Release{ID: "source", Version: 1}, config.Config{}, nil, nil)
			if err != nil {
				t.Error(err)
				return
			}
			if err := release(); err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()
	if len(r.hooks) != 1 || r.hooks[controlHookKey{"source", 1}].users != 0 {
		t.Fatal("same release was not reused")
	}
	ctx := context.Background()
	if (*AlertProcessorResources)(nil).context(ctx) != ctx {
		t.Fatal("nil resources changed context")
	}
	if r.context(ctx).Value(controlResourcesKey{}) != r {
		t.Fatal("resources missing from operation")
	}
}

func TestControlHookCloseDoesNotBlockOtherSources(t *testing.T) {
	for _, onRelease := range []bool{false, true} {
		name := "retire_idle_on_acquire"
		if onRelease {
			name = "retire_last_user_on_release"
		}
		t.Run(name, func(t *testing.T) {
			entered, unblock := make(chan struct{}), make(chan struct{})
			var unblockOnce sync.Once
			finishClose := func() { unblockOnce.Do(func() { close(unblock) }) }
			defer finishClose()
			var closed atomic.Int64
			closeErr := errors.New("retired hook close failed")
			old := controlHookKey{"source", 1}
			r := &AlertProcessorResources{hooks: map[controlHookKey]*controlHooks{old: {close: func() error {
				closed.Add(1)
				close(entered)
				<-unblock
				return closeErr
			}}}, latest: map[string]controlHookKey{}}
			var releaseOld func() error
			if onRelease {
				var err error
				_, releaseOld, err = r.acquireHooks(eventsource.Release{ID: "source", Version: 1}, config.Config{}, nil, nil)
				if err != nil {
					t.Fatal(err)
				}
				_, releaseNew, err := r.acquireHooks(eventsource.Release{ID: "source", Version: 2}, config.Config{}, nil, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := releaseNew(); err != nil {
						t.Error(err)
					}
				}()
			}
			done := make(chan error, 1)
			go func() {
				if onRelease {
					done <- releaseOld()
					return
				}
				_, release, err := r.acquireHooks(eventsource.Release{ID: "source", Version: 2}, config.Config{}, nil, nil)
				if err == nil {
					err = release()
				}
				done <- err
			}()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("retired hook did not start closing")
			}
			// 未放行旧来源 Close 时，另一来源必须完成 acquire/cache hit/release。
			otherDone := make(chan error, 1)
			go func() {
				_, release, err := r.acquireHooks(eventsource.Release{ID: "other", Version: 1}, config.Config{}, nil, nil)
				if err != nil {
					otherDone <- err
					return
				}
				cached, err := r.sourceRelease(t.Context(), nil, "other", 1)
				if err == nil && cached.ID != "other" {
					err = errors.New("source release cache miss")
				}
				otherDone <- errors.Join(err, release())
			}()
			select {
			case err := <-otherDone:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				finishClose()
				<-done
				<-otherDone
				t.Fatal("closing one source blocked another source")
			}
			finishClose()
			if err := <-done; !errors.Is(err, closeErr) {
				t.Fatalf("close error was lost: %v", err)
			}
			if closed.Load() != 1 {
				t.Fatalf("retired hook closed %d times", closed.Load())
			}
		})
	}
}

func BenchmarkControlHooksCachedAcquire(b *testing.B) {
	for _, sources := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprint(sources), func(b *testing.B) {
			r := &AlertProcessorResources{hooks: map[controlHookKey]*controlHooks{}, latest: map[string]controlHookKey{}}
			for i := range sources {
				key := controlHookKey{fmt.Sprint(i), 1}
				r.hooks[key] = &controlHooks{release: eventsource.Release{ID: key.source, Version: 1}, close: func() error { return nil }}
				r.latest[key.source] = key
			}
			release := eventsource.Release{ID: "0", Version: 1}
			b.ReportAllocs()
			for b.Loop() {
				_, done, err := r.acquireHooks(release, config.Config{}, nil, nil)
				if err != nil {
					b.Fatal(err)
				}
				if err := done(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
