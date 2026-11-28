// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package taskdispatch

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"linkd/internal/config"
	"linkd/internal/eventsource"
)

func TestTaskWriterIntegration(t *testing.T) {
	address := os.Getenv("LINKD_TEST_DISPATCH_REDIS")
	if address == "" {
		t.Skip("set LINKD_TEST_DISPATCH_REDIS")
	}
	client := redis.NewClient(&redis.Options{Addr: address})
	t.Cleanup(func() { _ = client.Close() })
	namespace := "task-writes-" + uuid.NewString()
	controller, err := NewController(t.Context(), client, eventsource.New(emptySources{}, config.DefaultSeverityConfig()), namespace)
	if err != nil {
		t.Fatal(err)
	}
	output := namespace + ":output"
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = client.Del(ctx, controller.key, controller.leader, output).Err()
	})
	original := Task{ID: "source:collector:0", Source: "source", Role: "collector", Worker: "worker", Epoch: 7, Phase: "running", Expires: time.Now().Add(time.Minute)}
	// writer 的初始过期时间故意设为过去；有效性必须取控制面已续租的当前快照。
	local := original
	local.Expires = time.Now().Add(-time.Hour)
	writer := NewTaskWriter(client, namespace, local)
	for _, tc := range []struct {
		name    string
		modify  func(*Task)
		allowed bool
	}{
		{"running", func(*Task) {}, true},
		{"starting", func(task *Task) { task.Phase = "starting" }, true},
		{"renewed in another timezone", func(task *Task) { task.Expires = time.Now().Add(time.Minute).In(time.FixedZone("test", -7*3600-30*60)) }, true},
		{"expired", func(task *Task) { task.Expires = time.Now().Add(-time.Second) }, false},
		{"stopping", func(task *Task) { task.Phase = "stopping" }, false},
		{"stopped", func(task *Task) { task.Phase = "stopped" }, false},
		{"new epoch same worker", func(task *Task) { task.Epoch++ }, false},
		{"another worker", func(task *Task) { task.Worker = "other" }, false},
		{"another source", func(task *Task) { task.Source = "other" }, false},
		{"another role", func(task *Task) { task.Role = "lifecycle" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task := original
			tc.modify(&task)
			if err := controller.update(t.Context(), func(state *State) error { state.Tasks[task.ID] = task; return nil }); err != nil {
				t.Fatal(err)
			}
			if err := client.Set(t.Context(), output, "previous", 0).Err(); err != nil {
				t.Fatal(err)
			}
			err := writer.Eval(t.Context(), `return redis.call('SET',KEYS[1],ARGV[1])`, []string{output}, "updated").Err()
			if tc.allowed {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "TASK_AUTHORITY_LOST") {
				t.Fatalf("unauthorized write: %v", err)
			}
			value, err := client.Get(t.Context(), output).Result()
			if err != nil {
				t.Fatal(err)
			}
			expected := "previous"
			if tc.allowed {
				expected = "updated"
			}
			if value != expected {
				t.Fatalf("value=%q want=%q", value, expected)
			}
		})
	}
	if err := controller.update(t.Context(), func(state *State) error { delete(state.Tasks, original.ID); return nil }); err != nil {
		t.Fatal(err)
	}
	if err := writer.Eval(t.Context(), `return redis.call('SET',KEYS[1],ARGV[1])`, []string{output}, "missing-task").Err(); err == nil {
		t.Fatal("removed task wrote output")
	}
	if err := client.Del(t.Context(), controller.key).Err(); err != nil {
		t.Fatal(err)
	}
	if err := writer.Eval(t.Context(), `return redis.call('SET',KEYS[1],ARGV[1])`, []string{output}, "missing-state").Err(); err == nil {
		t.Fatal("missing coordination state allowed write")
	}
	for _, value := range []string{"1970-01-01T00:00:00Z", "2000-02-29T08:00:00+08:00", "2024-02-29T23:59:59.123456789-07:30", "2026-11-14T12:01:02.001Z", "0001-01-01T00:00:00Z", "2100-03-01T00:00:00Z"} {
		expected, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			t.Fatal(err)
		}
		actual, err := client.Eval(t.Context(), taskExpiryScript+`return expires_millis(ARGV[1])`, nil, value).Int64()
		if err != nil || actual != expected.UnixMilli() {
			t.Fatalf("parse %s: %d want %d: %v", value, actual, expected.UnixMilli(), err)
		}
	}
}
