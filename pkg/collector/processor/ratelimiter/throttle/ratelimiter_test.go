// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package throttle

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewRateLimiter(t *testing.T) {
	t.Run("Noop", func(t *testing.T) {
		rl := New(Config{})
		assert.Equal(t, TypeNoop, rl.Type())
		assert.Equal(t, float32(0), rl.QPS())
		assert.True(t, rl.TryAccept())
	})

	t.Run("TokenBucket", func(t *testing.T) {
		rl := New(Config{Type: TypeTokenBucket})
		assert.Equal(t, TypeTokenBucket, rl.Type())
		assert.True(t, rl.TryAccept())
	})
}

func TestTokenBucketRateLimiter(t *testing.T) {
	t.Run("tokenBucket_5_10", func(t *testing.T) {
		rl := newTokenBucketRateLimiter(5, 10)
		assert.Equal(t, float32(5), rl.QPS())

		rejected := 0
		for i := 0; i < 100; i++ {
			if !rl.TryAccept() {
				rejected++
			}
		}
		assert.Equal(t, 90, rejected)
		rl.Stop()
	})

	t.Run("tokenBucket_10_20", func(t *testing.T) {
		rl := newTokenBucketRateLimiter(10, 20)
		rejected := 0
		for i := 0; i < 50; i++ {
			if !rl.TryAccept() {
				rejected++
			}
		}
		assert.Equal(t, 30, rejected)
		rl.Stop()
	})

	t.Run("tokenBucket_10_20", func(t *testing.T) {
		rl := newTokenBucketRateLimiter(10, 20)
		rejected := 0
		for i := 1; i <= 50; i++ {
			if i%10 == 0 {
				time.Sleep(time.Second)
			}
			if !rl.TryAccept() {
				rejected++
			}
		}
		assert.Equal(t, 0, rejected)
		rl.Stop()
	})

	t.Run("tokenBucket_0_0", func(t *testing.T) {
		rl := newTokenBucketRateLimiter(0, 0)
		rejected := 0
		for i := 1; i <= 50; i++ {
			if !rl.TryAccept() {
				rejected++
			}
		}
		assert.Equal(t, 0, rejected)
		rl.Stop()
	})

	t.Run("tokenBucket_-1_0", func(t *testing.T) {
		rl := newTokenBucketRateLimiter(-1, 0)
		rejected := 0
		for i := 1; i <= 50; i++ {
			if !rl.TryAccept() {
				rejected++
			}
		}
		assert.Equal(t, 50, rejected)
		rl.Stop()
	})
}

func TestBasicThrottle(t *testing.T) {
	r := newTokenBucketRateLimiter(1, 3)
	for i := 0; i < 3; i++ {
		if !r.TryAccept() {
			t.Error("unexpected false accept")
		}
	}
	if r.TryAccept() {
		t.Error("unexpected true accept")
	}
}

func TestIncrementThrottle(t *testing.T) {
	r := newTokenBucketRateLimiter(1, 1)
	if !r.TryAccept() {
		t.Error("unexpected false accept")
	}
	if r.TryAccept() {
		t.Error("unexpected true accept")
	}

	// Allow to refill
	time.Sleep(2 * time.Second)

	if !r.TryAccept() {
		t.Error("unexpected false accept")
	}
}
