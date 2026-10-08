// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package redislock

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/projection"
)

func TestRedisProjectionLocksCoordinateInstancesAndIsolateDeployments(t *testing.T) {
	address := os.Getenv("LINKD_TEST_REDIS_ADDRESS")
	if address == "" {
		t.Skip("set LINKD_TEST_REDIS_ADDRESS")
	}
	client := redis.NewClient(&redis.Options{Addr: address, Password: os.Getenv("LINKD_TEST_REDIS_PASSWORD"), ContextTimeoutEnabled: true, MaxRetries: -1, PoolSize: 4})
	defer func() { _ = client.Close() }()
	deployment := fmt.Sprintf("projection-lock-%d-%d", os.Getpid(), time.Now().UnixNano())
	first, err := New(client, deployment)
	if err != nil {
		t.Fatal(err)
	}
	second, err := New(client, deployment)
	if err != nil {
		t.Fatal(err)
	}
	other, err := New(client, deployment+"-other")
	if err != nil {
		t.Fatal(err)
	}
	key := strings.Repeat("a", 64)
	release, err := first.Acquire(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := second.Acquire(t.Context(), key); !errors.Is(err, projection.ErrBusy) {
		t.Fatal("instances did not coordinate", err)
	}
	isolated, err := other.Acquire(t.Context(), key)
	if err != nil {
		t.Fatal("deployments collide", err)
	}
	if err := isolated(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := release(t.Context()); err != nil {
		t.Fatal(err)
	}
	again, err := second.Acquire(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := again(ctx); err != nil {
			t.Error(err)
		}
	}()
	if err := release(t.Context()); err == nil {
		t.Fatal("old owner released another instance")
	}
	if _, err := first.Acquire(t.Context(), key); !errors.Is(err, projection.ErrBusy) {
		t.Fatal("new owner lost lease", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := first.Acquire(ctx, strings.Repeat("b", 64)); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation ignored", err)
	}
}
