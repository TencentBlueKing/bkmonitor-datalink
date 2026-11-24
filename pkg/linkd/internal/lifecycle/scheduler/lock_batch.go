// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package scheduler

import (
	"context"
	"fmt"

	redis "github.com/redis/go-redis/v9"
)

// AcquireBatch 用一次 pipeline 取得本批独立租约；返回的 token 也用于清理响应不确定的 SET。
// 调用方必须对全部返回租约调用 ReleaseBatch，不能因其中一项失败而遗留其他成员。
func (l *RedisLocker) AcquireBatch(ctx context.Context, keys []string) ([]Lease, []error) {
	leases, results := make([]Lease, len(keys)), make([]error, len(keys))
	client, ok := l.client.(interface{ Pipeline() redis.Pipeliner })
	if !ok {
		for i := range results {
			results[i] = fmt.Errorf("lifecycle locker requires Redis pipeline")
		}
		return leases, results
	}
	pipe := client.Pipeline()
	commands := make([]*redis.BoolCmd, len(keys))
	for i, key := range keys {
		token, err := l.tokenSource()
		if err != nil {
			results[i] = err
			continue
		}
		leases[i] = Lease{key: l.config.LockKeyPrefix + ":" + key, token: token}
		commands[i] = pipe.SetNX(ctx, leases[i].key, token, l.config.LockTTL)
	}
	_, pipelineErr := pipe.Exec(ctx)
	for i, c := range commands {
		if c != nil {
			var acquired bool
			acquired, results[i] = c.Result()
			if results[i] == nil && !acquired && pipelineErr != nil {
				results[i] = pipelineErr
			}
			if results[i] == nil && !acquired {
				results[i] = ErrLockBusy
			}
		}
	}
	return leases, results
}

// ReleaseBatch 一次 pipeline 执行逐租约 compare-token 删除，不会删除其他请求后来取得的锁。
func (l *RedisLocker) ReleaseBatch(ctx context.Context, leases []Lease) []error {
	results := make([]error, len(leases))
	client, ok := l.client.(interface{ Pipeline() redis.Pipeliner })
	if !ok {
		for i := range results {
			results[i] = fmt.Errorf("lifecycle locker requires Redis pipeline")
		}
		return results
	}
	pipe := client.Pipeline()
	commands := make([]*redis.Cmd, len(leases))
	script := redis.NewScript(releaseScript)
	first := true
	for i, lease := range leases {
		if lease.key != "" {
			run := script.EvalSha
			if first {
				run = script.Eval
				first = false
			}
			commands[i] = run(ctx, pipe, []string{lease.key}, lease.token)
		}
	}
	_, _ = pipe.Exec(ctx)
	for i, c := range commands {
		if c != nil {
			var n int64
			n, results[i] = c.Int64()
			if results[i] == nil && n != 1 {
				results[i] = ErrLeaseLost
			}
		}
	}
	return results
}
