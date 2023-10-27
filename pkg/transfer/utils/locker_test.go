// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package utils_test

import (
	"context"
	"os"
	"strconv"
	"sync"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// SemaphoreSuite :
type SemaphoreSuite struct {
	suite.Suite
}

// TestUsage :
func (s *SemaphoreSuite) RunTest(semaphore utils.Semaphore) {
	size := 3
	ch := make(chan bool, size)

	for i := 0; i < size; i++ {
		ch <- true
	}

	var wg sync.WaitGroup
	n := 10000
	for i := 0; i < size*n; i++ {
		wg.Add(1)
		s.NoError(semaphore.Acquire(context.Background(), 1))

		go func(i int) {
			select {
			case v := <-ch:
				ch <- v
			default:
				s.Fail("chan is empty")
			}
			semaphore.Release(1)
			wg.Done()
		}(i)
	}

	wg.Wait()
}

// TestSemaphoreSuite :
func TestSemaphoreSuite(t *testing.T) {
	suite.Run(t, new(SemaphoreSuite))
}

// ChainingSemaphoreSuite
type ChainingSemaphoreSuite struct {
	testsuite.ContextSuite
}

// TestOrdering
func (s *ChainingSemaphoreSuite) TestOrdering() {
	r := 0
	parent := testsuite.NewMockSemaphore(s.Ctrl)
	parent.EXPECT().Acquire(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, n int64) error {
		s.Equal(1, r)
		r++
		return nil
	})
	parent.EXPECT().Release(gomock.Any()).DoAndReturn(func(n int64) {
		s.Equal(2, r)
		r--
	})

	child := testsuite.NewMockSemaphore(s.Ctrl)
	child.EXPECT().Acquire(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, n int64) error {
		s.Equal(0, r)
		r++
		return nil
	})
	child.EXPECT().Release(gomock.Any()).DoAndReturn(func(n int64) {
		s.Equal(1, r)
		r--
	})

	chaining := utils.NewChainingSemaphore(parent, child)
	s.NoError(chaining.Acquire(s.CTX, 1))
	chaining.Release(1)
}

// TestTryAcquire1
func (s *ChainingSemaphoreSuite) TestTryAcquire1() {
	r := 0
	parent := testsuite.NewMockSemaphore(s.Ctrl)
	parent.EXPECT().TryAcquire(gomock.Any()).DoAndReturn(func(n int64) bool {
		s.Equal(1, r)
		return false
	})

	child := testsuite.NewMockSemaphore(s.Ctrl)
	child.EXPECT().TryAcquire(gomock.Any()).DoAndReturn(func(n int64) bool {
		s.Equal(0, r)
		r++
		return true
	})
	child.EXPECT().Release(gomock.Any()).DoAndReturn(func(n int64) {
		s.Equal(1, r)
		r--
	})
	chaining := utils.NewChainingSemaphore(parent, child)
	s.False(chaining.TryAcquire(1))
}

// TestTryAcquire2
func (s *ChainingSemaphoreSuite) TestTryAcquire2() {
	r := 0
	parent := testsuite.NewMockSemaphore(s.Ctrl)
	child := testsuite.NewMockSemaphore(s.Ctrl)
	child.EXPECT().TryAcquire(gomock.Any()).DoAndReturn(func(n int64) bool {
		s.Equal(0, r)
		return false
	})
	chaining := utils.NewChainingSemaphore(parent, child)
	s.False(chaining.TryAcquire(1))
}

// TestTryAcquire3
func (s *ChainingSemaphoreSuite) TestTryAcquire3() {
	r := 0
	parent := testsuite.NewMockSemaphore(s.Ctrl)
	parent.EXPECT().TryAcquire(gomock.Any()).DoAndReturn(func(n int64) bool {
		s.Equal(1, r)
		r++
		return true
	})

	child := testsuite.NewMockSemaphore(s.Ctrl)
	child.EXPECT().TryAcquire(gomock.Any()).DoAndReturn(func(n int64) bool {
		s.Equal(0, r)
		r++
		return true
	})

	chaining := utils.NewChainingSemaphore(parent, child)
	s.True(chaining.TryAcquire(1))
}

// TestChainingSemaphoreSuite
func TestChainingSemaphoreSuite(t *testing.T) {
	suite.Run(t, new(ChainingSemaphoreSuite))
}

func benchmarkSemaphore(b *testing.B, semaphore utils.Semaphore) {
	env := os.Getenv("BENCH_WORKERS")
	w, err := strconv.Atoi(env)
	if err != nil {
		w = 1
	}

	ctx := context.Background()

	b.SetParallelism(w)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = semaphore.Acquire(ctx, 1)
			semaphore.Release(1)
		}
	})
}

// BenchmarkWeightedLock_Lock :
func BenchmarkWeightedSemaphore(b *testing.B) {
	env := os.Getenv("BENCH_RESOURCE")
	v, err := strconv.Atoi(env)
	if err != nil {
		v = 1
	}

	benchmarkSemaphore(b, utils.NewWeightedSemaphore(int64(v)))
}
