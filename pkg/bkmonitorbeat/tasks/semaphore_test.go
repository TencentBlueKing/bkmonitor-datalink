// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package tasks_test

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"golang.org/x/sync/errgroup"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks"
)

func TestNoopSemaphore_Acquire(t *testing.T) {
	type args struct {
		ctx context.Context
		n   int64
	}
	tests := []struct {
		name    string
		args    args
		wantErr assert.ErrorAssertionFunc
	}{
		{
			"default",
			args{context.Background(), 1},
			func(t assert.TestingT, err error, i ...interface{}) bool {
				return false
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ns := &tasks.NoopSemaphore{}
			tt.wantErr(t, ns.Acquire(tt.args.ctx, tt.args.n), fmt.Sprintf("Acquire(%v, %v)", tt.args.ctx, tt.args.n))
		})
	}
}

func TestNoopSemaphore_Release(t *testing.T) {
	type args struct {
		n int64
	}
	tests := []struct {
		name string
		args args
	}{
		{
			"default",
			args{1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ns := &tasks.NoopSemaphore{}
			ns.Release(tt.args.n)
		})
	}
}

func TestNoopSemaphore_TryAcquire(t *testing.T) {
	type args struct {
		n int64
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			"default",
			args{1},
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ns := &tasks.NoopSemaphore{}
			assert.Equalf(t, tt.want, ns.TryAcquire(tt.args.n), "TryAcquire(%v)", tt.args.n)
		})
	}
}

func TestSemaphorePool_GetSemaphore(t *testing.T) {
	type fields struct{}
	type args struct {
		key1 string
		n1   int64
		key2 string
		n2   int64
	}
	tests := []struct {
		name   string
		fields fields
		args   args
	}{
		{
			"t1",
			fields{},
			args{"k1", 1, "k2", 2},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := tasks.NewSemaphorePool()
			s := p.GetSemaphore(tt.args.key1, tt.args.n1, tt.args.key2, tt.args.n2)
			assert.NotNilf(t, s, "GetSemaphore type(%v)", tt.args)
		})
	}
}

func Test_multiSemaphore_Acquire(t *testing.T) {
	t.Parallel()

	type acquireFunc func(ctx context.Context, wg *sync.WaitGroup, s tasks.Semaphore, n int64) bool

	tryAcquire := func(ctx context.Context, wg *sync.WaitGroup, s tasks.Semaphore, n int64) bool {
		defer wg.Done()
		ctx, cancel := context.WithTimeout(ctx, 10*time.Millisecond)
		defer cancel()
		err := s.Acquire(ctx, n)
		return err == nil
	}
	tryAcquireWithTimeout := func(t time.Duration) acquireFunc {
		return func(ctx context.Context, wg *sync.WaitGroup, s tasks.Semaphore, n int64) bool {
			defer wg.Done()
			t1 := time.Now()
			ctx, cancel := context.WithTimeout(ctx, t)
			defer cancel()
			err := s.Acquire(ctx, n)
			if err != nil {
				fmt.Println(err, t, time.Until(t1))
			}
			return err == nil
		}
	}
	tryAcquireRelease := func(ctx context.Context, wg *sync.WaitGroup, s tasks.Semaphore, n int64) bool {
		defer wg.Done()
		err := s.Acquire(ctx, n)
		if err == nil {
			time.Sleep(1 * time.Millisecond)
			s.Release(n)
		}
		return err == nil
	}
	tryAcquireReleaseForever := func(ctx context.Context, wg *sync.WaitGroup, s tasks.Semaphore, n int64) bool {
		err := s.Acquire(ctx, n)
		go func() {
			shouldRelease := err == nil
			defer func() {
				if shouldRelease {
					s.Release(n)
				}
				wg.Done()
			}()
			for {
				select {
				case <-ctx.Done():
					return
				default:
					time.Sleep(1 * time.Millisecond)
					if shouldRelease {
						s.Release(n)
					}
					shouldRelease = s.Acquire(ctx, n) == nil
				}
			}
		}()
		return err == nil
	}
	type weightArg struct {
		key1 string
		n1   int64
		key2 string
		n2   int64
	}
	type fields struct {
		weights       []weightArg
		executeWithGo bool
	}
	type args struct {
		ctx context.Context
	}
	type want struct {
		msg         string
		mIndex      int
		release     bool
		acquireFunc acquireFunc
		n           int64
		want        bool
	}
	duplicateWantList := func(w want, n int) []want {
		ws := make([]want, 0, n)
		for i := 0; i < n; i++ {
			ws = append(ws, w)
		}
		return ws
	}
	getTimeoutContext := func(t time.Duration) context.Context {
		ctx, _ := context.WithTimeout(context.Background(), t)
		return ctx
	}
	maxProcs := int64(runtime.GOMAXPROCS(0))
	tests := []struct {
		name   string
		fields fields
		args   args
		wants  []want
	}{
		{
			"同key限制并发",
			fields{
				weights: []weightArg{
					{
						"k0", 3,
						"k1", 2,
					},
					{
						"k0", 3,
						"k2", 4,
					},
				},
			},
			args{
				ctx: context.Background(),
			},
			[]want{
				{"1 k1剩余1，k0剩余2", 0, false, tryAcquire, 1, true},
				{"1 占用完k1，k0剩余1", 0, false, tryAcquire, 1, true},
				{"1 k1耗尽", 0, false, tryAcquire, 1, false},
				{"1 再占用完k0", 1, false, tryAcquire, 1, true},
				{"1 k0耗尽", 1, false, tryAcquire, 1, false},

				{"k1全部释放", 0, true, nil, 2, true},
				{"k2全部释放", 1, true, nil, 1, true},

				{"2 k1剩余1，k0剩余2", 0, false, tryAcquire, 1, true},
				{"2 占用完k1，k0剩余1", 0, false, tryAcquire, 1, true},
				{"2 k1耗尽", 0, false, tryAcquire, 1, false},
				{"2 再占用完k0", 1, false, tryAcquire, 1, true},
				{"2 k0耗尽", 1, false, tryAcquire, 1, false},
			},
		},
		{
			"超过限制并发不阻塞",
			fields{
				weights: []weightArg{
					{
						"k0", 2,
						"k1", 2,
					},
					{
						"k0", 2,
						"k2", 4,
					},
				},
				executeWithGo: true,
			},
			args{
				ctx: context.Background(),
			},
			append(
				duplicateWantList(want{"k0,k1", 0, false, tryAcquireRelease, 1, true}, 12),
				duplicateWantList(want{"k0,k2", 1, false, tryAcquireRelease, 1, true}, 12)...,
			),
		},
		{
			"满线程不阻塞",
			fields{
				weights: []weightArg{
					{
						"k0", maxProcs * 3,
						"k1", maxProcs,
					},
					{
						"k0", maxProcs * 3,
						"k2", maxProcs,
					},
				},
			},
			args{
				ctx: getTimeoutContext(time.Second),
			},
			append(
				duplicateWantList(want{"k1满线程不断申请", 0, false, tryAcquireReleaseForever, 1, true}, int(maxProcs)),
				append(
					[]want{
						{"k1仍可申请", 1, false, tryAcquireWithTimeout(time.Second), 1, true},
						{"k1释放", 1, true, nil, 1, true},
					},
					append(
						duplicateWantList(want{"k2满线程不断申请", 1, false, tryAcquireReleaseForever, 1, true}, int(maxProcs)),
						want{"k1一次性申请全部", 0, false, tryAcquireWithTimeout(time.Second), maxProcs, true},
						want{"k2一次性申请全部", 1, false, tryAcquireWithTimeout(time.Second), maxProcs, true},
						want{"k1耗尽", 0, false, tryAcquire, 1, false},
						want{"k2耗尽", 1, false, tryAcquire, 1, false},
						want{"k1全部释放", 0, true, nil, maxProcs, true},
						want{"k1释放后可用", 0, false, tryAcquire, 1, true},
						want{"k2仍为耗尽", 1, false, tryAcquire, 1, false},
						want{"k2全部释放", 1, true, nil, maxProcs, true},
						want{"k1可用", 0, false, tryAcquire, 1, true},
						want{"k2可用", 1, false, tryAcquire, 1, true},
					)...,
				)...,
			),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := tasks.NewSemaphorePool()
			ms := make([]tasks.Semaphore, 0, len(tt.fields.weights))
			for _, weight := range tt.fields.weights {
				m := p.GetSemaphore(weight.key1, weight.n1, weight.key2, weight.n2)
				ms = append(ms, m)
			}
			var wg sync.WaitGroup
			if tt.fields.executeWithGo {
				g, ctx := errgroup.WithContext(tt.args.ctx)
				for i, wt := range tt.wants {
					wtLoop := wt
					iLoop := i
					wg.Add(1)
					g.Go(func() error {
						if wtLoop.release {
							defer wg.Done()
							ms[wtLoop.mIndex].Release(wtLoop.n)
							return nil
						} else {
							if wtLoop.want != wt.acquireFunc(ctx, &wg, ms[wtLoop.mIndex], wtLoop.n) {
								return fmt.Errorf("wants: %d/%d-%s Acquire(%v)", iLoop, len(tt.wants), wtLoop.msg, wtLoop.n)
							}
						}
						return nil
					})
				}
				if err := g.Wait(); err != nil {
					t.Errorf("failed: %v", err)
				}
			} else {
				for i, wt := range tt.wants {
					wg.Add(1)
					if wt.release {
						ms[wt.mIndex].Release(wt.n)
						wg.Done()
					} else {
						assert.Equalf(t, wt.want, wt.acquireFunc(tt.args.ctx, &wg, ms[wt.mIndex], wt.n), "wants: %d/%d-%s Acquire(%v)", i, len(tt.wants), wt.msg, wt.n)
					}
				}
			}
			wg.Wait()
		})
	}
}
