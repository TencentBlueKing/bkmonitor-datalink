// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package redis

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"testing"
	"time"

	redis "github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/task"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/errors"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/timex"
)

// 使用独立 Redis 进程验证 Lua 原子操作和真实过期行为，需要安装 redis-server。
func newTestBroker(t *testing.T) *RDB {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := listener.Addr().String()
	_, port, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	require.NoError(t, listener.Close())
	command := exec.Command("redis-server", "--bind", "127.0.0.1", "--port", port,
		"--save", "", "--appendonly", "no", "--dir", t.TempDir())
	require.NoError(t, command.Start())
	t.Cleanup(func() {
		_ = command.Process.Kill()
		_ = command.Wait()
	})
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	require.Eventually(t, func() bool {
		return client.Ping(context.Background()).Err() == nil
	}, 5*time.Second, 10*time.Millisecond)
	return &RDB{client: client, clock: timex.NewTimeClock()}
}

func TestEnqueueMessageLifetime(t *testing.T) {
	broker := newTestBroker(t)
	ctx := context.Background()
	for _, test := range []struct {
		name    string
		unique  bool
		timeout int64
	}{
		{name: "普通任务排队超过执行时限", timeout: 1},
		{name: "唯一任务排队超过执行时限", unique: true, timeout: 1},
		{name: "普通任务仅指定截止时间"},
		{name: "唯一任务仅指定截止时间", unique: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			msg := &task.TaskMessage{ID: "periodic", Queue: test.name, Kind: "relation", Timeout: test.timeout}
			if test.timeout == 0 {
				msg.Deadline = time.Now().Add(time.Hour).Unix()
			}
			enqueue := func() error { return broker.Enqueue(ctx, msg) }
			if test.unique {
				msg.UniqueKey = common.TaskKeyPrefix(msg.Queue) + "unique"
				enqueue = func() error { return broker.EnqueueUnique(ctx, msg, time.Hour) }
			}
			require.NoError(t, enqueue())
			key := common.TaskKey(msg.Queue, msg.ID)
			require.Equal(t, time.Duration(-1), broker.client.TTL(ctx, key).Val())
			if test.timeout > 0 {
				// 等待真实时间经过 Timeout；执行时限从 Processor 开始执行时计算。
				time.Sleep(1100 * time.Millisecond)
			}
			require.Error(t, enqueue(), "未完成任务仍占用其任务 ID 和唯一键")
			got, _, err := broker.Dequeue(msg.Queue)
			require.NoError(t, err)
			require.Equal(t, msg, got)
			require.NoError(t, broker.Requeue(ctx, got))
			got, _, err = broker.Dequeue(msg.Queue)
			require.NoError(t, err)
			require.NoError(t, broker.Retry(ctx, got, time.Now().Add(-time.Second), "retry", true))
			require.NoError(t, broker.ForwardIfReady(msg.Queue))
			got, _, err = broker.Dequeue(msg.Queue)
			require.NoError(t, err)
			require.EqualValues(t, 1, got.Retried)
			require.NoError(t, broker.Done(ctx, got))
			require.Zero(t, broker.client.Exists(ctx, key).Val())
			require.NoError(t, enqueue(), "完成后必须释放周期任务 ID 和唯一键")
		})
	}
}

func TestDequeueMissingMessage(t *testing.T) {
	broker := newTestBroker(t)
	ctx := context.Background()
	for _, test := range []struct {
		name      string
		deleteKey bool
		unique    string
	}{
		{name: "消息键已经过期", deleteKey: true},
		{name: "只剩状态字段"},
		{name: "释放当前任务唯一键", unique: "periodic"},
		{name: "保留其他任务唯一键", unique: "other"},
	} {
		t.Run(test.name, func(t *testing.T) {
			msg := &task.TaskMessage{ID: "periodic", Queue: test.name, Kind: "relation", Timeout: 120}
			require.NoError(t, broker.Enqueue(ctx, msg))
			key := common.TaskKey(msg.Queue, msg.ID)
			// 旧消息过期后同一 ID 可能再次入队，清理时需要释放重复记录。
			require.NoError(t, broker.client.LPush(ctx, common.PendingKey(msg.Queue), msg.ID).Err())
			if test.deleteKey {
				require.NoError(t, broker.client.PExpire(ctx, key, time.Millisecond).Err())
				require.Eventually(t, func() bool {
					return broker.client.Exists(ctx, key).Val() == 0
				}, time.Second, time.Millisecond)
			} else {
				require.NoError(t, broker.client.HDel(ctx, key, "msg").Err())
			}
			uniqueKey := common.TaskKeyPrefix(msg.Queue) + "unique"
			if test.unique != "" {
				require.NoError(t, broker.client.HSet(ctx, key, "unique_key", uniqueKey).Err())
				require.NoError(t, broker.client.Set(ctx, uniqueKey, test.unique, time.Hour).Err())
			}
			_, _, err := broker.Dequeue(msg.Queue)
			require.ErrorContains(t, err, "MISSING TASK MESSAGE")
			require.NotErrorIs(t, err, errors.ErrNoProcessableTask)
			require.Zero(t, broker.client.Exists(ctx, key).Val())
			require.Zero(t, broker.client.LLen(ctx, common.ActiveKey(msg.Queue)).Val())
			require.Zero(t, broker.client.LLen(ctx, common.PendingKey(msg.Queue)).Val())
			require.Zero(t, broker.client.ZCard(ctx, common.LeaseKey(msg.Queue)).Val())
			if test.unique == "other" {
				require.Equal(t, "other", broker.client.Get(ctx, uniqueKey).Val())
			} else {
				require.Zero(t, broker.client.Exists(ctx, uniqueKey).Val())
			}
			require.NoError(t, broker.Enqueue(ctx, msg))
			got, _, err := broker.Dequeue(msg.Queue)
			require.NoError(t, err)
			require.Equal(t, msg, got)
		})
	}
}

func TestLegacyMessageTTLAndRetention(t *testing.T) {
	broker := newTestBroker(t)
	ctx := context.Background()
	for _, test := range []struct {
		name    string
		recover bool
		unique  bool
	}{
		{name: "出队清除旧消息TTL"},
		{name: "恢复清除旧消息TTL", recover: true},
		{name: "唯一任务出队清除旧消息TTL", unique: true},
		{name: "唯一任务恢复清除旧消息TTL", recover: true, unique: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			msg := &task.TaskMessage{ID: "periodic", Queue: test.name, Kind: "relation", Timeout: 120, Retention: 1}
			if test.unique {
				msg.UniqueKey = common.TaskKeyPrefix(msg.Queue) + "unique"
				require.NoError(t, broker.EnqueueUnique(ctx, msg, time.Hour))
			} else {
				require.NoError(t, broker.Enqueue(ctx, msg))
			}
			key := common.TaskKey(msg.Queue, msg.ID)
			if test.recover {
				_, _, err := broker.Dequeue(msg.Queue)
				require.NoError(t, err)
				require.NoError(t, broker.client.ZAdd(ctx, common.LeaseKey(msg.Queue), &redis.Z{
					Score: float64(time.Now().Add(-time.Second).Unix()), Member: msg.ID,
				}).Err())
			}
			// 写入旧版本遗留的 TTL，验证接管任务后取消过期时间。
			require.NoError(t, broker.client.Expire(ctx, key, time.Minute).Err())
			if test.recover {
				count, err := broker.RecoverExpired(time.Now(), msg.Queue)
				require.NoError(t, err)
				require.Equal(t, 1, count)
				require.Equal(t, time.Duration(-1), broker.client.TTL(ctx, key).Val())
			}
			got, _, err := broker.Dequeue(msg.Queue)
			require.NoError(t, err)
			require.Equal(t, time.Duration(-1), broker.client.TTL(ctx, key).Val())
			require.NoError(t, broker.MarkAsComplete(ctx, got))
			require.NoError(t, broker.DeleteExpiredCompletedTasks(msg.Queue))
			require.Equal(t, "completed", broker.client.HGet(ctx, key, "state").Val())
			// 完成结果继续遵守 Retention；取消消息 TTL 不影响现有结果清理。
			time.Sleep(1100 * time.Millisecond)
			require.NoError(t, broker.DeleteExpiredCompletedTasks(msg.Queue))
			require.Zero(t, broker.client.Exists(ctx, key).Val())
			require.Zero(t, broker.client.ZCard(ctx, common.CompletedKey(msg.Queue)).Val())
			if test.unique {
				require.Zero(t, broker.client.Exists(ctx, msg.UniqueKey).Val())
			}
		})
	}
}

func TestRecoverExpired(t *testing.T) {
	broker := newTestBroker(t)
	ctx := context.Background()
	for _, test := range []struct {
		name        string
		state       string
		leaseOffset time.Duration
		active      bool
		missing     string
		unique      string
		wantCount   int
		wantError   bool
	}{
		{name: "过期的执行中任务", state: "active", active: true, leaseOffset: -time.Hour, wantCount: 1},
		{name: "租约边界", state: "active", active: true, wantCount: 1},
		{name: "租约仍然有效", state: "active", active: true, leaseOffset: time.Hour},
		{name: "已完成任务的旧租约", state: "completed", leaseOffset: -time.Hour},
		{name: "已排队任务的旧租约", state: "pending", leaseOffset: -time.Hour},
		{name: "缺少执行队列记录", state: "active", leaseOffset: -time.Hour},
		{name: "缺少消息字段", state: "active", active: true, missing: "msg", leaseOffset: -time.Hour, wantError: true},
		{name: "缺少整个消息键", active: true, missing: "key", leaseOffset: -time.Hour, wantError: true},
		{name: "释放当前任务唯一键", state: "active", active: true, missing: "msg", unique: "periodic", leaseOffset: -time.Hour, wantError: true},
		{name: "保留其他任务唯一键", state: "active", active: true, missing: "msg", unique: "other", leaseOffset: -time.Hour, wantError: true},
		{name: "缺少消息但租约有效", state: "active", active: true, missing: "msg", leaseOffset: time.Hour},
	} {
		t.Run(test.name, func(t *testing.T) {
			msg := &task.TaskMessage{ID: "periodic", Queue: test.name, Kind: "relation", Timeout: 120}
			require.NoError(t, broker.Enqueue(ctx, msg))
			key := common.TaskKey(msg.Queue, msg.ID)
			pendingKey := common.PendingKey(msg.Queue)
			activeKey := common.ActiveKey(msg.Queue)
			leaseKey := common.LeaseKey(msg.Queue)
			uniqueKey := common.TaskKeyPrefix(msg.Queue) + "unique"
			require.NoError(t, broker.client.Del(ctx, pendingKey).Err())
			require.NoError(t, broker.client.HSet(ctx, key, "state", test.state).Err())
			if test.active {
				require.NoError(t, broker.client.LPush(ctx, activeKey, msg.ID).Err())
			}
			if test.missing == "key" {
				require.NoError(t, broker.client.Del(ctx, key).Err())
			} else if test.missing == "msg" {
				require.NoError(t, broker.client.HDel(ctx, key, "msg").Err())
			}
			if test.unique != "" {
				require.NoError(t, broker.client.HSet(ctx, key, "unique_key", uniqueKey).Err())
				require.NoError(t, broker.client.Set(ctx, uniqueKey, test.unique, time.Hour).Err())
			}
			now := time.Now()
			require.NoError(t, broker.client.ZAdd(ctx, leaseKey, &redis.Z{
				Score: float64(now.Add(test.leaseOffset).Unix()), Member: msg.ID,
			}).Err())
			count, err := broker.RecoverExpired(now, msg.Queue)
			require.Equal(t, test.wantCount, count)
			if test.wantError {
				require.ErrorContains(t, err, "missing task message")
				require.Zero(t, broker.client.Exists(ctx, key).Val())
				require.Zero(t, broker.client.LLen(ctx, activeKey).Val())
				if test.unique == "other" {
					require.Equal(t, "other", broker.client.Get(ctx, uniqueKey).Val())
				} else {
					require.Zero(t, broker.client.Exists(ctx, uniqueKey).Val())
				}
			} else {
				require.NoError(t, err)
				wantState := test.state
				if test.wantCount > 0 {
					wantState = "pending"
				}
				require.Equal(t, wantState, broker.client.HGet(ctx, key, "state").Val())
			}
			require.Equal(t, int64(test.wantCount), broker.client.LLen(ctx, pendingKey).Val())
			wantLeases := int64(0)
			if test.leaseOffset > 0 {
				wantLeases = 1
			}
			require.Equal(t, wantLeases, broker.client.ZCard(ctx, leaseKey).Val())
			count, err = broker.RecoverExpired(now, msg.Queue)
			require.NoError(t, err)
			require.Zero(t, count)
			if test.wantError {
				require.NoError(t, broker.Enqueue(ctx, msg), "清理后允许调度同一个周期任务")
			}
		})
	}
}

func TestRecoverExpiredBatch(t *testing.T) {
	broker := newTestBroker(t)
	ctx := context.Background()
	for _, test := range []struct {
		name    string
		missing bool
	}{
		{name: "清理一百条旧租约后继续恢复"},
		{name: "报告缺失消息前保留已经恢复的数量", missing: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			now := time.Now()
			queue := test.name
			for i := 0; i < 101; i++ {
				id := fmt.Sprintf("task-%03d", i)
				msg := &task.TaskMessage{ID: id, Queue: queue, Kind: "relation", Timeout: 120}
				require.NoError(t, broker.Enqueue(ctx, msg))
				state := "completed"
				if test.missing || i == 100 {
					state = "active"
					require.NoError(t, broker.client.LPush(ctx, common.ActiveKey(queue), id).Err())
				}
				require.NoError(t, broker.client.HSet(ctx, common.TaskKey(queue, id), "state", state).Err())
				if test.missing && i == 50 {
					require.NoError(t, broker.client.HDel(ctx, common.TaskKey(queue, id), "msg").Err())
				}
				require.NoError(t, broker.client.ZAdd(ctx, common.LeaseKey(queue), &redis.Z{
					Score: float64(now.Add(-time.Hour).Unix()), Member: id,
				}).Err())
			}
			require.NoError(t, broker.client.Del(ctx, common.PendingKey(queue)).Err())
			count, err := broker.RecoverExpired(now, queue)
			if test.missing {
				require.ErrorContains(t, err, "missing task message")
				require.Equal(t, 50, count)
			} else {
				require.NoError(t, err)
				require.Zero(t, count)
			}
			next, err := broker.RecoverExpired(now, queue)
			require.NoError(t, err)
			want := 1
			if test.missing {
				want = 100
			}
			require.Equal(t, want, count+next)
			require.Equal(t, int64(want), broker.client.LLen(ctx, common.PendingKey(queue)).Val())
			require.Zero(t, broker.client.ZCard(ctx, common.LeaseKey(queue)).Val())
		})
	}
}
