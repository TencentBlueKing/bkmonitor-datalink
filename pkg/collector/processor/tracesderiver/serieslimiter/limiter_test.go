// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package serieslimiter

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/labels"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/random"
)

func TestLimiterExceeded(t *testing.T) {
	limiter := New(10, time.Hour)
	defer limiter.Stop()

	ids := []int32{1001}
	exceeded := 0
	for i := 0; i < 100; i++ {
		for _, id := range ids {
			h := labels.HashFromMap(random.Dimensions(6))
			ok := limiter.Set(id, h)
			if !ok {
				exceeded++
			}
		}
	}
	assert.True(t, exceeded > 80)
}

func TestLimiterNotExceeded(t *testing.T) {
	limiter := New(100, time.Hour)
	defer limiter.Stop()

	ids := []int32{1001}
	exceeded := 0
	for i := 0; i < 100; i++ {
		for _, id := range ids {
			h := labels.HashFromMap(random.Dimensions(6))
			ok := limiter.Set(id, h)
			if !ok {
				exceeded++
			}
		}
	}
	assert.Equal(t, exceeded, 0)
}

func TestLimiterGcOk(t *testing.T) {
	limiter := New(10, time.Second)
	defer limiter.Stop()

	ids := []int32{1001}
	exceeded := 0
	for i := 0; i < 20; i++ {
		for _, id := range ids {
			h := labels.HashFromMap(random.Dimensions(6))
			ok := limiter.Set(id, h)
			if !ok {
				exceeded++
			}
			if i == 9 {
				time.Sleep(3 * time.Second)
			}
		}
	}
	assert.Equal(t, exceeded, 0)
}

func TestLimiterGcNotYet(t *testing.T) {
	limiter := New(10, 2*time.Second)
	defer limiter.Stop()

	ids := []int32{1001}
	exceeded := 0
	for i := 0; i < 20; i++ {
		for _, id := range ids {
			h := labels.HashFromMap(random.Dimensions(6))
			ok := limiter.Set(id, h)
			if !ok {
				exceeded++
			}
			if i == 9 {
				time.Sleep(time.Second)
			}
		}
	}
	assert.True(t, exceeded > 8)
}

func BenchmarkLimiterSet(b *testing.B) {
	const n = 1000000
	limiter := New(n, time.Minute)
	defer limiter.Stop()

	start := time.Now()
	wg := sync.WaitGroup{}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < n; j++ {
				h := labels.HashFromMap(random.Dimensions(1))
				limiter.Set(1, h)
			}
		}()
	}
	wg.Wait()
	b.Logf("set elapsed time: %v", time.Since(start))
}
