// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package daemon

import (
	"context"
	"sync"
	"testing"

	"github.com/alicebob/miniredis/v2"
	redis "github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/task"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
)

func TestEnqueueActiveProfilingTaskIsIdempotent(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- enqueueActiveProfilingTask(context.Background(), client)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	items, err := client.SMembers(context.Background(), common.DaemonTaskKey()).Result()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d daemon tasks, want 1", len(items))
	}
	var stored task.SerializerTask
	if err := jsonx.Unmarshal([]byte(items[0]), &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Kind != ActiveProfilingTaskKind || stored.Options.Queue != activeProfilingQueue || string(stored.Payload) != "{}" {
		t.Fatalf("unexpected daemon task: %+v", stored)
	}
}

func TestEnsureActiveProfilingTaskSkipsOtherQueues(t *testing.T) {
	if err := EnsureActiveProfilingTask(context.Background(), []string{"default"}); err != nil {
		t.Fatal(err)
	}
}

func TestEnqueueActiveProfilingTaskReturnsRedisError(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	_ = client.Close()

	if err := enqueueActiveProfilingTask(context.Background(), client); err == nil {
		t.Fatal("enqueue should return the Redis error")
	}
}
