// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// from bmw redis command

package redis

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	retry "github.com/avast/retry-go"
	redis "github.com/go-redis/redis/v8"
	"github.com/spf13/cast"

	common "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	task "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/task"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/errors"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/timex"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
	redisUtils "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/register/redis"
)

// set ttl
const statsTTL = 90 * 24 * time.Hour

const LeaseDuration = 30 * time.Minute

// RDB is a client interface to query and mutate task queues.
type RDB struct {
	client redis.UniversalClient
	clock  timex.Clock
}

var (
	brokerInstance *RDB
	brokerOnce     sync.Once
)

// GetRDB Get the redis broker client
func GetRDB() *RDB {
	if brokerInstance != nil {
		return brokerInstance
	}

	brokerOnce.Do(func() {
		var client redis.UniversalClient
		var err error

		err = retry.Do(
			func() error {
				client, err = redisUtils.NewRedisClient(
					context.Background(),
					&redisUtils.Option{
						Mode:             config.BrokerRedisMode,
						Host:             config.BrokerRedisStandaloneHost,
						Port:             config.BrokerRedisStandalonePort,
						Password:         config.BrokerRedisStandalonePassword,
						SentinelAddress:  config.BrokerRedisSentinelAddress,
						MasterName:       config.BrokerRedisSentinelMasterName,
						SentinelPassword: config.BrokerRedisSentinelPassword,
						Db:               config.BrokerRedisDatabase,
						DialTimeout:      config.BrokerRedisDialTimeout,
						ReadTimeout:      config.BrokerRedisReadTimeout,
					},
				)
				if err != nil {
					return err
				}
				return nil
			},
			retry.Attempts(3),
			retry.Delay(5*time.Second),
		)
		// 因为是必要依赖，如果有错误，直接异常
		if err != nil {
			logger.Fatalf("failed to create redis broker client, error: %s", err)
		}

		brokerInstance = &RDB{client: client, clock: timex.NewTimeClock()}
	})

	return brokerInstance
}

// Open opens a connection
func (r *RDB) Open() error {
	return nil
}

// Close closes the connection with redis server.
func (r *RDB) Close() error {
	return r.client.Close()
}

// Client returns the reference to underlying redis client.
func (r *RDB) Client() redis.UniversalClient {
	return r.client
}

// SetClock sets the clock used by RDB to the given clock.
func (r *RDB) SetClock(c timex.Clock) {
	r.clock = c
}

// Ping checks the connection with redis server.
func (r *RDB) Ping() error {
	return r.client.Ping(context.Background()).Err()
}

func (r *RDB) runScript(ctx context.Context, op errors.Op, script *redis.Script, keys []string, args ...any) error {
	if err := script.Run(ctx, r.client, keys, args...).Err(); err != nil {
		return errors.E(op, errors.Internal, fmt.Sprintf("redis eval error: %v", err))
	}
	return nil
}

// Runs the given script with keys and args and returns the script's return value as int64.
func (r *RDB) runScriptWithErrorCode(ctx context.Context, op errors.Op, script *redis.Script, keys []string, args ...any) (int64, error) {
	res, err := script.Run(ctx, r.client, keys, args...).Result()
	if err != nil {
		return 0, errors.E(op, errors.Unknown, fmt.Sprintf("redis eval error: %v", err))
	}
	n, ok := res.(int64)
	if !ok {
		return 0, errors.E(op, errors.Internal, fmt.Sprintf("unexpected return value from Lua script: %v", res))
	}
	return n, nil
}

// enqueueCmd enqueues a given task message.
//
// Input:
// KEYS[1] -> bmw:{<qname>}:t:<task_id>
// KEYS[2] -> bmw:{<qname>}:pending
// --
// ARGV[1] -> task message data
// ARGV[2] -> task ID
// ARGV[3] -> current unix time in nsec
//
// Output:
// Returns 1 if successfully enqueued
// Returns 0 if task ID already exists
var enqueueCmd = redis.NewScript(`
if redis.call("EXISTS", KEYS[1]) == 1 then
	return 0
end
redis.call("HSET", KEYS[1],
           "msg", ARGV[1],
           "state", "pending",
           "pending_since", ARGV[3])
redis.call("EXPIRE", KEYS[1], ARGV[4])
redis.call("LPUSH", KEYS[2], ARGV[2])
return 1
`)

// Enqueue adds the given task to the pending list of the queue.
func (r *RDB) Enqueue(ctx context.Context, msg *task.TaskMessage) error {
	var op errors.Op = "rdb.Enqueue"
	encoded, err := task.EncodeMessage(msg)
	if err != nil {
		return errors.E(op, errors.Unknown, fmt.Sprintf("cannot encode message: %v", err))
	}
	if err := r.client.SAdd(ctx, common.AllQueues, msg.Queue).Err(); err != nil {
		return errors.E(op, errors.Unknown, &errors.RedisCommandError{Command: "sadd", Err: err})
	}
	keys := []string{
		common.TaskKey(msg.Queue, msg.ID),
		common.PendingKey(msg.Queue),
	}
	argv := []any{
		encoded,
		msg.ID,
		r.clock.Now().UnixNano(),
		msg.Timeout,
	}
	n, err := r.runScriptWithErrorCode(ctx, op, enqueueCmd, keys, argv...)
	if err != nil {
		return err
	}
	if n == 0 {
		return errors.E(op, errors.AlreadyExists, errors.ErrTaskIdConflict)
	}
	return nil
}

// enqueueUniqueCmd enqueues the task message if the task is unique.
//
// KEYS[1] -> unique key
// KEYS[2] -> bmw:{<qname>}:t:<taskid>
// KEYS[3] -> bmw:{<qname>}:pending
// --
// ARGV[1] -> task ID
// ARGV[2] -> uniqueness lock TTL
// ARGV[3] -> task message data
// ARGV[4] -> current unix time in nsec
//
// Output:
// Returns 1 if successfully enqueued
// Returns 0 if task ID conflicts with another task
// Returns -1 if task unique key already exists
var enqueueUniqueCmd = redis.NewScript(`
local ok = redis.call("SET", KEYS[1], ARGV[1], "NX", "EX", ARGV[2])
if not ok then
  return -1
end
if redis.call("EXISTS", KEYS[2]) == 1 then
  return 0
end
redis.call("HSET", KEYS[2],
           "msg", ARGV[3],
           "state", "pending",
           "pending_since", ARGV[4],
           "unique_key", KEYS[1])
redis.call("EXPIRE", KEYS[2], ARGV[5])
redis.call("LPUSH", KEYS[3], ARGV[1])
return 1
`)

// EnqueueUnique inserts the given task if the task's uniqueness lock can be acquired.
// It returns ErrDuplicateTask if the lock cannot be acquired.
func (r *RDB) EnqueueUnique(ctx context.Context, msg *task.TaskMessage, ttl time.Duration) error {
	var op errors.Op = "rdb.EnqueueUnique"
	encoded, err := task.EncodeMessage(msg)
	if err != nil {
		return errors.E(op, errors.Internal, "cannot encode task message: %v", err)
	}
	if err := r.client.SAdd(ctx, common.AllQueues, msg.Queue).Err(); err != nil {
		return errors.E(op, errors.Unknown, &errors.RedisCommandError{Command: "sadd", Err: err})
	}
	keys := []string{
		msg.UniqueKey,
		common.TaskKey(msg.Queue, msg.ID),
		common.PendingKey(msg.Queue),
	}
	argv := []any{
		msg.ID,
		int(ttl.Seconds()),
		encoded,
		r.clock.Now().UnixNano(),
		msg.Timeout,
	}
	n, err := r.runScriptWithErrorCode(ctx, op, enqueueUniqueCmd, keys, argv...)
	if err != nil {
		return err
	}
	if n == -1 {
		return errors.E(op, errors.AlreadyExists, errors.ErrDuplicateTask, msg.UniqueKey)
	}
	if n == 0 {
		return errors.E(op, errors.AlreadyExists, errors.ErrTaskIdConflict, msg.UniqueKey)
	}
	return nil
}

// Input:
// KEYS[1] -> bmw:{<qname>}:pending
// KEYS[2] -> bmw:{<qname>}:paused
// KEYS[3] -> bmw:{<qname>}:active
// KEYS[4] -> bmw:{<qname>}:lease
// --
// ARGV[1] -> initial lease expiration Unix time
// ARGV[2] -> task key prefix
//
// Output:
// Returns nil if no processable task is found in the given queue.
// Returns an encoded TaskMessage.
//
// Note: dequeueCmd checks whether a queue is paused first, before
// calling RPOPLPUSH to pop a task from the queue.
var dequeueCmd = redis.NewScript(`
if redis.call("EXISTS", KEYS[2]) == 0 then
	local id = redis.call("RPOPLPUSH", KEYS[1], KEYS[3])
	if id then
		local key = ARGV[2] .. id
		redis.call("HSET", key, "state", "active")
		redis.call("HDEL", key, "pending_since")
		redis.call("ZADD", KEYS[4], ARGV[1], id)
		return redis.call("HGET", key, "msg")
	end
end
return nil`)

// Dequeue queries given queues in order and pops a task message
// off a queue if one exists and returns the message and its lease expiration time.
// Dequeue skips a queue if the queue is paused.
// If all queues are empty, ErrNoProcessableTask error is returned.
func (r *RDB) Dequeue(qnames ...string) (msg *task.TaskMessage, leaseExpirationTime time.Time, err error) {
	var op errors.Op = "rdb.Dequeue"
	for _, qname := range qnames {
		keys := []string{
			common.PendingKey(qname),
			common.PausedKey(qname),
			common.ActiveKey(qname),
			common.LeaseKey(qname),
		}
		leaseExpirationTime = r.clock.Now().Add(LeaseDuration)
		argv := []any{
			leaseExpirationTime.Unix(),
			common.TaskKeyPrefix(qname),
		}
		res, err := dequeueCmd.Run(context.Background(), r.client, keys, argv...).Result()
		if err == redis.Nil {
			logger.Debugf("No processable task found in queue %s, keys: %v, args: %v", qname, keys, argv)
			continue
		} else if err != nil {
			return nil, time.Time{}, errors.E(op, errors.Unknown, fmt.Sprintf("redis eval error: %v", err))
		}
		encoded, err := cast.ToStringE(res)
		if err != nil {
			return nil, time.Time{}, errors.E(
				op, errors.Internal,
				fmt.Sprintf("cast error: unexpected return value from Lua script: %v", res),
			)
		}
		if msg, err = task.DecodeMessage([]byte(encoded)); err != nil {
			return nil, time.Time{}, errors.E(op, errors.Internal, fmt.Sprintf("cannot decode message: %v", err))
		}
		return msg, leaseExpirationTime, nil
	}
	return nil, time.Time{}, errors.E(op, errors.NotFound, errors.ErrNoProcessableTask)
}

// KEYS[1] -> bmw:{<qname>}:active
// KEYS[2] -> bmw:{<qname>}:lease
// KEYS[3] -> bmw:{<qname>}:t:<task_id>
// KEYS[4] -> bmw:{<qname>}:processed:<yyyy-mm-dd>
// KEYS[5] -> bmw:{<qname>}:processed
// -------
// ARGV[1] -> task ID
// ARGV[2] -> stats expiration timestamp
// ARGV[3] -> max int64 value
var doneCmd = redis.NewScript(`
redis.call("LREM", KEYS[1], 0, ARGV[1])
redis.call("ZREM", KEYS[2], ARGV[1])
if redis.call("DEL", KEYS[3]) == 0 then
  return redis.error_reply("NOT FOUND")
end
local n = redis.call("INCR", KEYS[4])
if tonumber(n) == 1 then
	redis.call("EXPIREAT", KEYS[4], ARGV[2])
end
local total = redis.call("GET", KEYS[5])
if tonumber(total) == tonumber(ARGV[3]) then
	redis.call("SET", KEYS[5], 1)
else
	redis.call("INCR", KEYS[5])
end
return redis.status_reply("OK")
`)

// KEYS[1] -> bmw:{<qname>}:active
// KEYS[2] -> bmw:{<qname>}:lease
// KEYS[3] -> bmw:{<qname>}:t:<task_id>
// KEYS[4] -> bmw:{<qname>}:processed:<yyyy-mm-dd>
// KEYS[5] -> bmw:{<qname>}:processed
// KEYS[6] -> unique key
// -------
// ARGV[1] -> task ID
// ARGV[2] -> stats expiration timestamp
// ARGV[3] -> max int64 value
var doneUniqueCmd = redis.NewScript(`
redis.call("LREM", KEYS[1], 0, ARGV[1])
redis.call("ZREM", KEYS[2], ARGV[1])
if redis.call("DEL", KEYS[3]) == 0 then
  return redis.error_reply("NOT FOUND")
end
local n = redis.call("INCR", KEYS[4])
if tonumber(n) == 1 then
	redis.call("EXPIREAT", KEYS[4], ARGV[2])
end
local total = redis.call("GET", KEYS[5])
if tonumber(total) == tonumber(ARGV[3]) then
	redis.call("SET", KEYS[5], 1)
else
	redis.call("INCR", KEYS[5])
end
if redis.call("GET", KEYS[6]) == ARGV[1] then
  redis.call("DEL", KEYS[6])
end
return redis.status_reply("OK")
`)

// Done removes the task from active queue and deletes the task.
// It removes a uniqueness lock acquired by the task, if any.
func (r *RDB) Done(ctx context.Context, msg *task.TaskMessage) error {
	var op errors.Op = "rdb.Done"
	now := r.clock.Now()
	expireAt := now.Add(statsTTL)
	keys := []string{
		common.ActiveKey(msg.Queue),
		common.LeaseKey(msg.Queue),
		common.TaskKey(msg.Queue, msg.ID),
		common.ProcessedKey(msg.Queue, now),
		common.ProcessedTotalKey(msg.Queue),
	}
	argv := []any{
		msg.ID,
		expireAt.Unix(),
		int64(math.MaxInt64),
	}
	// Note: We cannot pass empty unique key when running this script in redis-cluster.
	if len(msg.UniqueKey) > 0 {
		keys = append(keys, msg.UniqueKey)
		return r.runScript(ctx, op, doneUniqueCmd, keys, argv...)
	}
	return r.runScript(ctx, op, doneCmd, keys, argv...)
}

// KEYS[1] -> bmw:{<qname>}:active
// KEYS[2] -> bmw:{<qname>}:lease
// KEYS[3] -> bmw:{<qname>}:completed
// KEYS[4] -> bmw:{<qname>}:t:<task_id>
// KEYS[5] -> bmw:{<qname>}:processed:<yyyy-mm-dd>
// KEYS[6] -> bmw:{<qname>}:processed
//
// ARGV[1] -> task ID
// ARGV[2] -> stats expiration timestamp
// ARGV[3] -> task expiration time in unix time
// ARGV[4] -> task message data
// ARGV[5] -> max int64 value
var markAsCompleteCmd = redis.NewScript(`
if redis.call("LREM", KEYS[1], 0, ARGV[1]) == 0 then
  return redis.error_reply("NOT FOUND")
end
if redis.call("ZREM", KEYS[2], ARGV[1]) == 0 then
  return redis.error_reply("NOT FOUND")
end
if redis.call("ZADD", KEYS[3], ARGV[3], ARGV[1]) ~= 1 then
  return redis.error_reply("INTERNAL")
end
redis.call("HSET", KEYS[4], "msg", ARGV[4], "state", "completed")
local n = redis.call("INCR", KEYS[5])
if tonumber(n) == 1 then
	redis.call("EXPIREAT", KEYS[5], ARGV[2])
end
local total = redis.call("GET", KEYS[6])
if tonumber(total) == tonumber(ARGV[5]) then
	redis.call("SET", KEYS[6], 1)
else
	redis.call("INCR", KEYS[6])
end
return redis.status_reply("OK")
`)

// KEYS[1] -> bmw:{<qname>}:active
// KEYS[2] -> bmw:{<qname>}:lease
// KEYS[3] -> bmw:{<qname>}:completed
// KEYS[4] -> bmw:{<qname>}:t:<task_id>
// KEYS[5] -> bmw:{<qname>}:processed:<yyyy-mm-dd>
// KEYS[6] -> bmw:{<qname>}:processed
// KEYS[7] -> bmw:{<qname>}:unique:{<checksum>}
//
// ARGV[1] -> task ID
// ARGV[2] -> stats expiration timestamp
// ARGV[3] -> task expiration time in unix time
// ARGV[4] -> task message data
// ARGV[5] -> max int64 value
var markAsCompleteUniqueCmd = redis.NewScript(`
if redis.call("LREM", KEYS[1], 0, ARGV[1]) == 0 then
  return redis.error_reply("NOT FOUND")
end
if redis.call("ZREM", KEYS[2], ARGV[1]) == 0 then
  return redis.error_reply("NOT FOUND")
end
if redis.call("ZADD", KEYS[3], ARGV[3], ARGV[1]) ~= 1 then
  return redis.error_reply("INTERNAL")
end
redis.call("HSET", KEYS[4], "msg", ARGV[4], "state", "completed")
local n = redis.call("INCR", KEYS[5])
if tonumber(n) == 1 then
	redis.call("EXPIREAT", KEYS[5], ARGV[2])
end
local total = redis.call("GET", KEYS[6])
if tonumber(total) == tonumber(ARGV[5]) then
	redis.call("SET", KEYS[6], 1)
else
	redis.call("INCR", KEYS[6])
end
if redis.call("GET", KEYS[7]) == ARGV[1] then
  redis.call("DEL", KEYS[7])
end
return redis.status_reply("OK")
`)

// MarkAsComplete removes the task from active queue to mark the task as completed.
// It removes a uniqueness lock acquired by the task, if any.
func (r *RDB) MarkAsComplete(ctx context.Context, msg *task.TaskMessage) error {
	var op errors.Op = "rdb.MarkAsComplete"
	now := r.clock.Now()
	statsExpireAt := now.Add(statsTTL)
	msg.CompletedAt = now.Unix()
	encoded, err := task.EncodeMessage(msg)
	if err != nil {
		return errors.E(op, errors.Unknown, fmt.Sprintf("cannot encode message: %v", err))
	}
	keys := []string{
		common.ActiveKey(msg.Queue),
		common.LeaseKey(msg.Queue),
		common.CompletedKey(msg.Queue),
		common.TaskKey(msg.Queue, msg.ID),
		common.ProcessedKey(msg.Queue, now),
		common.ProcessedTotalKey(msg.Queue),
	}
	argv := []any{
		msg.ID,
		statsExpireAt.Unix(),
		now.Unix() + msg.Retention,
		encoded,
		int64(math.MaxInt64),
	}
	// Note: We cannot pass empty unique key when running this script in redis-cluster.
	if len(msg.UniqueKey) > 0 {
		keys = append(keys, msg.UniqueKey)
		return r.runScript(ctx, op, markAsCompleteUniqueCmd, keys, argv...)
	}
	return r.runScript(ctx, op, markAsCompleteCmd, keys, argv...)
}

// KEYS[1] -> bmw:{<qname>}:active
// KEYS[2] -> bmw:{<qname>}:lease
// KEYS[3] -> bmw:{<qname>}:pending
// KEYS[4] -> bmw:{<qname>}:t:<task_id>
// ARGV[1] -> task ID
// Note: Use RPUSH to push to the head of the queue.
var requeueCmd = redis.NewScript(`
if redis.call("LREM", KEYS[1], 0, ARGV[1]) == 0 then
  return redis.error_reply("NOT FOUND")
end
if redis.call("ZREM", KEYS[2], ARGV[1]) == 0 then
  return redis.error_reply("NOT FOUND")
end
redis.call("RPUSH", KEYS[3], ARGV[1])
redis.call("HSET", KEYS[4], "state", "pending")
return redis.status_reply("OK")`)

// Requeue moves the task from active queue to the specified queue.
func (r *RDB) Requeue(ctx context.Context, msg *task.TaskMessage) error {
	var op errors.Op = "rdb.Requeue"
	keys := []string{
		common.ActiveKey(msg.Queue),
		common.LeaseKey(msg.Queue),
		common.PendingKey(msg.Queue),
		common.TaskKey(msg.Queue, msg.ID),
	}
	return r.runScript(ctx, op, requeueCmd, keys, msg.ID)
}

// KEYS[1] -> bmw:{<qname>}:t:<task_id>
// KEYS[2] -> bmw:{<qname>}:scheduled
// -------
// ARGV[1] -> task message data
// ARGV[2] -> process_at time in Unix time
// ARGV[3] -> task ID
//
// Output:
// Returns 1 if successfully enqueued
// Returns 0 if task ID already exists
var scheduleCmd = redis.NewScript(`
if redis.call("EXISTS", KEYS[1]) == 1 then
	return 0
end
redis.call("HSET", KEYS[1],
           "msg", ARGV[1],
           "state", "scheduled")
redis.call("ZADD", KEYS[2], ARGV[2], ARGV[3])
return 1
`)

// Schedule adds the task to the scheduled set to be processed in the future.
func (r *RDB) Schedule(ctx context.Context, msg *task.TaskMessage, processAt time.Time) error {
	var op errors.Op = "rdb.Schedule"
	encoded, err := task.EncodeMessage(msg)
	if err != nil {
		return errors.E(op, errors.Unknown, fmt.Sprintf("cannot encode message: %v", err))
	}
	if err := r.client.SAdd(ctx, common.AllQueues, msg.Queue).Err(); err != nil {
		return errors.E(op, errors.Unknown, &errors.RedisCommandError{Command: "sadd", Err: err})
	}
	keys := []string{
		common.TaskKey(msg.Queue, msg.ID),
		common.ScheduledKey(msg.Queue),
	}
	argv := []any{
		encoded,
		processAt.Unix(),
		msg.ID,
	}
	n, err := r.runScriptWithErrorCode(ctx, op, scheduleCmd, keys, argv...)
	if err != nil {
		return err
	}
	if n == 0 {
		return errors.E(op, errors.AlreadyExists, errors.ErrTaskIdConflict)
	}
	return nil
}

// KEYS[1] -> unique key
// KEYS[2] -> bmw:{<qname>}:t:<task_id>
// KEYS[3] -> bmw:{<qname>}:scheduled
// -------
// ARGV[1] -> task ID
// ARGV[2] -> uniqueness lock TTL
// ARGV[3] -> score (process_at timestamp)
// ARGV[4] -> task message
//
// Output:
// Returns 1 if successfully scheduled
// Returns 0 if task ID already exists
// Returns -1 if task unique key already exists
var scheduleUniqueCmd = redis.NewScript(`
local ok = redis.call("SET", KEYS[1], ARGV[1], "NX", "EX", ARGV[2])
if not ok then
  return -1
end
if redis.call("EXISTS", KEYS[2]) == 1 then
  return 0
end
redis.call("HSET", KEYS[2],
           "msg", ARGV[4],
           "state", "scheduled",
           "unique_key", KEYS[1])
redis.call("ZADD", KEYS[3], ARGV[3], ARGV[1])
return 1
`)

// ScheduleUnique adds the task to the backlog queue to be processed in the future,
// if the uniqueness lock can be acquired.
// It returns ErrDuplicateTask if the lock cannot be acquired.
func (r *RDB) ScheduleUnique(ctx context.Context, msg *task.TaskMessage, processAt time.Time, ttl time.Duration) error {
	var op errors.Op = "rdb.ScheduleUnique"
	encoded, err := task.EncodeMessage(msg)
	if err != nil {
		return errors.E(op, errors.Internal, fmt.Sprintf("cannot encode task message: %v", err))
	}
	if err := r.client.SAdd(ctx, common.AllQueues, msg.Queue).Err(); err != nil {
		return errors.E(op, errors.Unknown, &errors.RedisCommandError{Command: "sadd", Err: err})
	}
	keys := []string{
		msg.UniqueKey,
		common.TaskKey(msg.Queue, msg.ID),
		common.ScheduledKey(msg.Queue),
	}
	argv := []any{
		msg.ID,
		int(ttl.Seconds()),
		processAt.Unix(),
		encoded,
	}
	n, err := r.runScriptWithErrorCode(ctx, op, scheduleUniqueCmd, keys, argv...)
	if err != nil {
		return err
	}
	if n == -1 {
		return errors.E(op, errors.AlreadyExists, errors.ErrDuplicateTask)
	}
	if n == 0 {
		return errors.E(op, errors.AlreadyExists, errors.ErrTaskIdConflict)
	}
	return nil
}

// KEYS[1] -> bmw:{<qname>}:t:<task_id>
// KEYS[2] -> bmw:{<qname>}:active
// KEYS[3] -> bmw:{<qname>}:lease
// KEYS[4] -> bmw:{<qname>}:retry
// KEYS[5] -> bmw:{<qname>}:processed:<yyyy-mm-dd>
// KEYS[6] -> bmw:{<qname>}:failed:<yyyy-mm-dd>
// KEYS[7] -> bmw:{<qname>}:processed
// KEYS[8] -> bmw:{<qname>}:failed
// -------
// ARGV[1] -> task ID
// ARGV[2] -> updated task.TaskMessage value
// ARGV[3] -> retry_at UNIX timestamp
// ARGV[4] -> stats expiration timestamp
// ARGV[5] -> is_failure (bool)
// ARGV[6] -> max int64 value
var retryCmd = redis.NewScript(`
if redis.call("LREM", KEYS[2], 0, ARGV[1]) == 0 then
  return redis.error_reply("NOT FOUND")
end
if redis.call("ZREM", KEYS[3], ARGV[1]) == 0 then
  return redis.error_reply("NOT FOUND")
end
redis.call("ZADD", KEYS[4], ARGV[3], ARGV[1])
redis.call("HSET", KEYS[1], "msg", ARGV[2], "state", "retry")
if tonumber(ARGV[5]) == 1 then
	local n = redis.call("INCR", KEYS[5])
	if tonumber(n) == 1 then
		redis.call("EXPIREAT", KEYS[5], ARGV[4])
	end
	local m = redis.call("INCR", KEYS[6])
	if tonumber(m) == 1 then
		redis.call("EXPIREAT", KEYS[6], ARGV[4])
	end
    local total = redis.call("GET", KEYS[7])
    if tonumber(total) == tonumber(ARGV[6]) then
    	redis.call("SET", KEYS[7], 1)
    	redis.call("SET", KEYS[8], 1)
    else
    	redis.call("INCR", KEYS[7])
    	redis.call("INCR", KEYS[8])
    end
end
return redis.status_reply("OK")`)

// Retry moves the task from active to retry queue.
// It also annotates the message with the given error message and
// if isFailure is true increments the retried counter.
func (r *RDB) Retry(ctx context.Context, msg *task.TaskMessage, processAt time.Time, errMsg string, isFailure bool) error {
	var op errors.Op = "rdb.Retry"
	now := r.clock.Now()
	modified := *msg
	if isFailure {
		modified.Retried++
	}
	modified.ErrorMsg = errMsg
	modified.LastFailedAt = now.Unix()
	encoded, err := task.EncodeMessage(&modified)
	if err != nil {
		return errors.E(op, errors.Internal, fmt.Sprintf("cannot encode message: %v", err))
	}
	expireAt := now.Add(statsTTL)
	keys := []string{
		common.TaskKey(msg.Queue, msg.ID),
		common.ActiveKey(msg.Queue),
		common.LeaseKey(msg.Queue),
		common.RetryKey(msg.Queue),
		common.ProcessedKey(msg.Queue, now),
		common.FailedKey(msg.Queue, now),
		common.ProcessedTotalKey(msg.Queue),
		common.FailedTotalKey(msg.Queue),
	}
	argv := []any{
		msg.ID,
		encoded,
		processAt.Unix(),
		expireAt.Unix(),
		isFailure,
		int64(math.MaxInt64),
	}
	return r.runScript(ctx, op, retryCmd, keys, argv...)
}

const (
	maxArchiveSize           = 10000 // maximum number of tasks in archive
	archivedExpirationInDays = 90    // number of days before an archived task gets deleted permanently
)

// KEYS[1] -> bmw:{<qname>}:t:<task_id>
// KEYS[2] -> bmw:{<qname>}:active
// KEYS[3] -> bmw:{<qname>}:lease
// KEYS[4] -> bmw:{<qname>}:archived
// KEYS[5] -> bmw:{<qname>}:processed:<yyyy-mm-dd>
// KEYS[6] -> bmw:{<qname>}:failed:<yyyy-mm-dd>
// KEYS[7] -> bmw:{<qname>}:processed
// KEYS[8] -> bmw:{<qname>}:failed
// -------
// ARGV[1] -> task ID
// ARGV[2] -> updated task.TaskMessage value
// ARGV[3] -> died_at UNIX timestamp
// ARGV[4] -> cutoff timestamp (e.g., 90 days ago)
// ARGV[5] -> max number of tasks in archive (e.g., 100)
// ARGV[6] -> stats expiration timestamp
// ARGV[7] -> max int64 value
var archiveCmd = redis.NewScript(`
redis.call("LREM", KEYS[2], 0, ARGV[1])
redis.call("ZREM", KEYS[3], ARGV[1])
redis.call("ZADD", KEYS[4], ARGV[3], ARGV[1])
redis.call("ZREMRANGEBYSCORE", KEYS[4], "-inf", ARGV[4])
redis.call("ZREMRANGEBYRANK", KEYS[4], 0, -ARGV[5])
redis.call("DEL", KEYS[1])
local n = redis.call("INCR", KEYS[5])
if tonumber(n) == 1 then
	redis.call("EXPIREAT", KEYS[5], ARGV[6])
end
local m = redis.call("INCR", KEYS[6])
if tonumber(m) == 1 then
	redis.call("EXPIREAT", KEYS[6], ARGV[6])
end
local total = redis.call("GET", KEYS[7])
if tonumber(total) == tonumber(ARGV[7]) then
   	redis.call("SET", KEYS[7], 1)
   	redis.call("SET", KEYS[8], 1)
else
  	redis.call("INCR", KEYS[7])
   	redis.call("INCR", KEYS[8])
end
return redis.status_reply("OK")`)

// Archive sends the given task to archive, attaching the error message to the task.
// It also trims the archive by timestamp and set size.
func (r *RDB) Archive(ctx context.Context, msg *task.TaskMessage, errMsg string) error {
	var op errors.Op = "rdb.Archive"
	now := r.clock.Now()
	modified := *msg
	modified.ErrorMsg = errMsg
	modified.LastFailedAt = now.Unix()
	encoded, err := task.EncodeMessage(&modified)
	if err != nil {
		return errors.E(op, errors.Internal, fmt.Sprintf("cannot encode message: %v", err))
	}
	cutoff := now.AddDate(0, 0, -archivedExpirationInDays)
	expireAt := now.Add(statsTTL)
	keys := []string{
		common.TaskKey(msg.Queue, msg.ID),
		common.ActiveKey(msg.Queue),
		common.LeaseKey(msg.Queue),
		common.ArchivedKey(msg.Queue),
		common.ProcessedKey(msg.Queue, now),
		common.FailedKey(msg.Queue, now),
		common.ProcessedTotalKey(msg.Queue),
		common.FailedTotalKey(msg.Queue),
	}
	argv := []any{
		msg.ID,
		encoded,
		now.Unix(),
		cutoff.Unix(),
		maxArchiveSize,
		expireAt.Unix(),
		int64(math.MaxInt64),
	}
	return r.runScript(ctx, op, archiveCmd, keys, argv...)
}

// ForwardIfReady checks scheduled and retry sets of the given queues
// and move any tasks that are ready to be processed to the pending set.
func (r *RDB) ForwardIfReady(qnames ...string) error {
	var op errors.Op = "rdb.ForwardIfReady"
	for _, qname := range qnames {
		if err := r.forwardAll(qname); err != nil {
			return errors.E(op, errors.CanonicalCode(err), err)
		}
	}
	return nil
}

// KEYS[1] -> source queue (e.g. bmw:{<qname>:scheduled or bmw:{<qname>}:retry})
// KEYS[2] -> bmw:{<qname>}:pending
// ARGV[1] -> current unix time in seconds
// ARGV[2] -> task key prefix
// ARGV[3] -> current unix time in nsec
// Note: Script moves tasks up to 100 at a time to keep the runtime of script short.
var forwardCmd = redis.NewScript(`
local ids = redis.call("ZRANGEBYSCORE", KEYS[1], "-inf", ARGV[1], "LIMIT", 0, 100)
for _, id in ipairs(ids) do
	local taskKey = ARGV[2] .. id
	redis.call("LPUSH", KEYS[2], id)
	redis.call("ZREM", KEYS[1], id)
	redis.call("HSET", taskKey,
				"state", "pending",
				"pending_since", ARGV[3])
end
return table.getn(ids)`)

// forward moves tasks with a score less than the current unix time from the delayed (i.e. scheduled | retry) zset
func (r *RDB) forward(delayedKey, pendingKey, taskKeyPrefix string) (int, error) {
	now := r.clock.Now()
	keys := []string{delayedKey, pendingKey}
	argv := []any{
		now.Unix(),
		taskKeyPrefix,
		now.UnixNano(),
	}
	res, err := forwardCmd.Run(context.Background(), r.client, keys, argv...).Result()
	if err != nil {
		return 0, errors.E(errors.Internal, fmt.Sprintf("redis eval error: %v", err))
	}
	n, err := cast.ToIntE(res)
	if err != nil {
		return 0, errors.E(errors.Internal, fmt.Sprintf("cast error: Lua script returned unexpected value: %v", res))
	}
	return n, nil
}

// forwardAll checks for tasks in scheduled/retry state that are ready to be run, and updates
func (r *RDB) forwardAll(qname string) (err error) {
	delayedKeys := []string{common.ScheduledKey(qname), common.RetryKey(qname)}
	pendingKey := common.PendingKey(qname)
	taskKeyPrefix := common.TaskKeyPrefix(qname)
	for _, delayedKey := range delayedKeys {
		n := 1
		for n != 0 {
			n, err = r.forward(delayedKey, pendingKey, taskKeyPrefix)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// KEYS[1] -> bmw:{<qname>}:completed
// ARGV[1] -> current time in unix time
// ARGV[2] -> task key prefix
// ARGV[3] -> batch size (i.e. maximum number of tasks to delete)
//
// Returns the number of tasks deleted.
var deleteExpiredCompletedTasksCmd = redis.NewScript(`
local ids = redis.call("ZRANGEBYSCORE", KEYS[1], "-inf", ARGV[1], "LIMIT", 0, tonumber(ARGV[3]))
for _, id in ipairs(ids) do
	redis.call("DEL", ARGV[2] .. id)
	redis.call("ZREM", KEYS[1], id)
end
return table.getn(ids)`)

// DeleteExpiredCompletedTasks checks for any expired tasks in the given queue's completed set,
// and delete all expired tasks.
func (r *RDB) DeleteExpiredCompletedTasks(qname string) error {
	// Note: Do this operation in fix batches to prevent long running script.
	const batchSize = 100
	for {
		n, err := r.deleteExpiredCompletedTasks(qname, batchSize)
		if err != nil {
			return err
		}
		if n == 0 {
			return nil
		}
	}
}

// deleteExpiredCompletedTasks runs the lua script to delete expired deleted task with the specified
// batch size. It reports the number of tasks deleted.
func (r *RDB) deleteExpiredCompletedTasks(qname string, batchSize int) (int64, error) {
	var op errors.Op = "rdb.DeleteExpiredCompletedTasks"
	keys := []string{common.CompletedKey(qname)}
	argv := []any{
		r.clock.Now().Unix(),
		common.TaskKeyPrefix(qname),
		batchSize,
	}
	res, err := deleteExpiredCompletedTasksCmd.Run(context.Background(), r.client, keys, argv...).Result()
	if err != nil {
		return 0, errors.E(op, errors.Internal, fmt.Sprintf("redis eval error: %v", err))
	}
	n, ok := res.(int64)
	if !ok {
		return 0, errors.E(op, errors.Internal, fmt.Sprintf("unexpected return value from Lua script: %v", res))
	}
	return n, nil
}

// KEYS[1] -> bmw:{<qname>}:lease
// ARGV[1] -> cutoff in unix time
// ARGV[2] -> task key prefix
var listLeaseExpiredCmd = redis.NewScript(`
local res = {}
local ids = redis.call("ZRANGEBYSCORE", KEYS[1], "-inf", ARGV[1])
for _, id in ipairs(ids) do
	local key = ARGV[2] .. id
	table.insert(res, redis.call("HGET", key, "msg"))
end
return res
`)

// ListLeaseExpired returns a list of task messages with an expired lease from the given queues.
func (r *RDB) ListLeaseExpired(cutoff time.Time, qnames ...string) ([]*task.TaskMessage, error) {
	var op errors.Op = "rdb.ListLeaseExpired"
	var msgs []*task.TaskMessage
	for _, qname := range qnames {
		res, err := listLeaseExpiredCmd.Run(context.Background(), r.client,
			[]string{common.LeaseKey(qname)},
			cutoff.Unix(), common.TaskKeyPrefix(qname)).Result()
		if err != nil {
			return nil, errors.E(op, errors.Internal, fmt.Sprintf("redis eval error: %v", err))
		}
		data, err := cast.ToStringSliceE(res)
		if err != nil {
			return nil, errors.E(op, errors.Internal, fmt.Sprintf("cast error: Lua script returned unexpected value: %v", res))
		}
		for _, s := range data {
			msg, err := task.DecodeMessage([]byte(s))
			if err != nil {
				return nil, errors.E(op, errors.Internal, fmt.Sprintf("cannot decode message: %v", err))
			}
			msgs = append(msgs, msg)
		}
	}
	return msgs, nil
}

// ExtendLease extends the lease for the given tasks by LeaseDuration (30s).
// It returns a new expiration time if the operation was successful.
func (r *RDB) ExtendLease(qname string, ids ...string) (expirationTime time.Time, err error) {
	expireAt := r.clock.Now().Add(LeaseDuration)
	var zs []*redis.Z
	for _, id := range ids {
		zs = append(zs, &redis.Z{Member: id, Score: float64(expireAt.Unix())})
	}
	// Use XX option to only update elements that already exist; Don't add new elements
	// TODO: Consider adding GT option to ensure we only "extend" the lease.
	// TODO Ceveat is that GT is supported from redis v6.2.0 or above.
	err = r.client.ZAddXX(context.Background(), common.LeaseKey(qname), zs...).Err()
	if err != nil {
		return time.Time{}, err
	}
	return expireAt, nil
}

// KEYS[1]  -> bmw:servers:{<host:pid:sid>}
// KEYS[2]  -> bmw:workers:{<host:pid:sid>}
// ARGV[1]  -> TTL in seconds
// ARGV[2]  -> server info
// ARGV[3:] -> alternate key-value pair of (worker id, worker data)
// Note: Add key to ZSET with expiration time as score.
// ref: https://github.com/antirez/redis/issues/135#issuecomment-2361996
var writeServerStateCmd = redis.NewScript(`
redis.call("SETEX", KEYS[1], ARGV[1], ARGV[2])
redis.call("DEL", KEYS[2])
for i = 3, table.getn(ARGV)-1, 2 do
	redis.call("HSET", KEYS[2], ARGV[i], ARGV[i+1])
end
redis.call("EXPIRE", KEYS[2], ARGV[1])
return redis.status_reply("OK")`)

// WriteServerState writes server state data to redis with expiration set to the value ttl.
func (r *RDB) WriteServerState(info *common.ServerInfo, workers []*common.WorkerInfo, ttl time.Duration) error {
	var op errors.Op = "rdb.WriteServerState"
	ctx := context.Background()
	bytes, err := common.EncodeServerInfo(info)
	if err != nil {
		return errors.E(op, errors.Internal, fmt.Sprintf("cannot encode server info: %v", err))
	}
	exp := r.clock.Now().Add(ttl).UTC()
	args := []any{ttl.Seconds(), bytes} // args to the lua script
	for _, w := range workers {
		bytes, err := common.EncodeWorkerInfo(w)
		if err != nil {
			continue // skip bad data
		}
		args = append(args, w.ID, bytes)
	}
	skey := common.ServerInfoKey(info.Host, info.PID, info.ServerID)
	wkey := common.WorkersKey(info.Host, info.PID, info.ServerID)
	if err := r.client.ZAdd(ctx, common.AllServers, &redis.Z{Score: float64(exp.Unix()), Member: skey}).Err(); err != nil {
		return errors.E(op, errors.Unknown, &errors.RedisCommandError{Command: "sadd", Err: err})
	}
	if err := r.client.ZAdd(ctx, common.AllWorkers, &redis.Z{Score: float64(exp.Unix()), Member: wkey}).Err(); err != nil {
		return errors.E(op, errors.Unknown, &errors.RedisCommandError{Command: "zadd", Err: err})
	}
	return r.runScript(ctx, op, writeServerStateCmd, []string{skey, wkey}, args...)
}

// KEYS[1] -> bmw:servers:{<host:pid:sid>}
// KEYS[2] -> bmw:workers:{<host:pid:sid>}
var clearServerStateCmd = redis.NewScript(`
redis.call("DEL", KEYS[1])
redis.call("DEL", KEYS[2])
return redis.status_reply("OK")`)

// ClearServerState deletes server state data from redis.
func (r *RDB) ClearServerState(host string, pid int, serverID string) error {
	var op errors.Op = "rdb.ClearServerState"
	ctx := context.Background()
	skey := common.ServerInfoKey(host, pid, serverID)
	wkey := common.WorkersKey(host, pid, serverID)
	if err := r.client.ZRem(ctx, common.AllServers, skey).Err(); err != nil {
		return errors.E(op, errors.Internal, &errors.RedisCommandError{Command: "zrem", Err: err})
	}
	if err := r.client.ZRem(ctx, common.AllWorkers, wkey).Err(); err != nil {
		return errors.E(op, errors.Internal, &errors.RedisCommandError{Command: "zrem", Err: err})
	}
	return r.runScript(ctx, op, clearServerStateCmd, []string{skey, wkey})
}

// KEYS[1]  -> bmw:schedulers:{<schedulerID>}
// ARGV[1]  -> TTL in seconds
// ARGV[2:] -> schedler entries
var writeSchedulerEntriesCmd = redis.NewScript(`
redis.call("DEL", KEYS[1])
for i = 2, #ARGV do
	redis.call("LPUSH", KEYS[1], ARGV[i])
end
redis.call("EXPIRE", KEYS[1], ARGV[1])
return redis.status_reply("OK")`)

// WriteSchedulerEntries writes scheduler entries data to redis with expiration set to the value ttl.
func (r *RDB) WriteSchedulerEntries(schedulerID string, entries []*common.SchedulerEntry, ttl time.Duration) error {
	var op errors.Op = "rdb.WriteSchedulerEntries"
	ctx := context.Background()
	args := []any{ttl.Seconds()}
	for _, e := range entries {
		bytes, err := common.EncodeSchedulerEntry(e)
		if err != nil {
			continue // skip bad data
		}
		args = append(args, bytes)
	}
	exp := r.clock.Now().Add(ttl).UTC()
	key := common.SchedulerEntriesKey(schedulerID)
	err := r.client.ZAdd(ctx, common.AllSchedulers, &redis.Z{Score: float64(exp.Unix()), Member: key}).Err()
	if err != nil {
		return errors.E(op, errors.Unknown, &errors.RedisCommandError{Command: "zadd", Err: err})
	}
	return r.runScript(ctx, op, writeSchedulerEntriesCmd, []string{key}, args...)
}

// ClearSchedulerEntries deletes scheduler entries data from redis.
func (r *RDB) ClearSchedulerEntries(scheduelrID string) error {
	var op errors.Op = "rdb.ClearSchedulerEntries"
	ctx := context.Background()
	key := common.SchedulerEntriesKey(scheduelrID)
	if err := r.client.ZRem(ctx, common.AllSchedulers, key).Err(); err != nil {
		return errors.E(op, errors.Unknown, &errors.RedisCommandError{Command: "zrem", Err: err})
	}
	if err := r.client.Del(ctx, key).Err(); err != nil {
		return errors.E(op, errors.Unknown, &errors.RedisCommandError{Command: "del", Err: err})
	}
	return nil
}

// KEYS[1] -> bmw:scheduler_history:<entryID>
// ARGV[1] -> enqueued_at timestamp
// ARGV[2] -> serialized SchedulerEnqueueEvent data
// ARGV[3] -> max number of events to be persisted
var recordSchedulerEnqueueEventCmd = redis.NewScript(`
redis.call("ZREMRANGEBYRANK", KEYS[1], 0, -ARGV[3])
redis.call("ZADD", KEYS[1], ARGV[1], ARGV[2])
return redis.status_reply("OK")`)

// Maximum number of enqueue events to store per entry.
const maxEvents = 1000

// RecordSchedulerEnqueueEvent records the time when the given task was enqueued.
func (r *RDB) RecordSchedulerEnqueueEvent(entryID string, event *common.SchedulerEnqueueEvent) error {
	var op errors.Op = "rdb.RecordSchedulerEnqueueEvent"
	ctx := context.Background()
	data, err := common.EncodeSchedulerEnqueueEvent(event)
	if err != nil {
		return errors.E(op, errors.Internal, fmt.Sprintf("cannot encode scheduler enqueue event: %v", err))
	}
	keys := []string{
		common.SchedulerHistoryKey(entryID),
	}
	argv := []any{
		event.EnqueuedAt.Unix(),
		data,
		maxEvents,
	}
	return r.runScript(ctx, op, recordSchedulerEnqueueEventCmd, keys, argv...)
}

// ClearSchedulerHistory deletes the enqueue event history for the given scheduler entry.
func (r *RDB) ClearSchedulerHistory(entryID string) error {
	var op errors.Op = "rdb.ClearSchedulerHistory"
	ctx := context.Background()
	key := common.SchedulerHistoryKey(entryID)
	if err := r.client.Del(ctx, key).Err(); err != nil {
		return errors.E(op, errors.Unknown, &errors.RedisCommandError{Command: "del", Err: err})
	}
	return nil
}

// WriteResult writes the given result data for the specified task.
func (r *RDB) WriteResult(qname, taskID string, data []byte) (int, error) {
	var op errors.Op = "rdb.WriteResult"
	ctx := context.Background()
	taskKey := common.TaskKey(qname, taskID)
	if err := r.client.HSet(ctx, taskKey, "result", data).Err(); err != nil {
		return 0, errors.E(op, errors.Unknown, &errors.RedisCommandError{Command: "hset", Err: err})
	}
	return len(data), nil
}
