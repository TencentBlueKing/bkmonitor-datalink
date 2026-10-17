// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package recentalert

import (
	"context"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/store"
)

// PutCurrentSnapshots 将同一来源的可信快照显式 pipeline，保留每个 SET 的结果与原 TTL。
func (s *Store) PutCurrentSnapshots(ctx context.Context, current []store.StoredAlert) []error {
	results := make([]error, len(current))
	client, ok := s.client.(interface {
		Pipelined(context.Context, func(redis.Pipeliner) error) ([]redis.Cmder, error)
	})
	if !ok {
		for i, item := range current {
			results[i] = s.PutCurrentFromSnapshot(ctx, item)
		}
		return results
	}
	// 1. 复用单项快照编码和来源 key；无效项不影响同批其他成员。
	keys, payloads := make([]string, len(current)), make([][]byte, len(current))
	for i, item := range current {
		payloads[i], results[i] = encodeSnapshot(item)
		if results[i] == nil {
			keys[i], results[i] = s.currentKey(activeKey(item.Alert))
		}
	}
	// 2. 每项独立 SET，无跨成员事务；传输失败由成员操作幂等重试。
	commands := make([]*redis.StatusCmd, len(current))
	_, pipelineErr := client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		for i := range current {
			if results[i] == nil {
				commands[i] = pipe.Set(ctx, keys[i], payloads[i], s.config.TTL())
			}
		}
		return nil
	})
	for i, cmd := range commands {
		outcome := "invalid"
		if cmd != nil {
			results[i] = cmd.Err()
			if results[i] == nil && pipelineErr != nil && cmd.Val() != "OK" {
				results[i] = pipelineErr
			}
			outcome = "succeeded"
			if results[i] != nil {
				outcome = "failed"
			}
		}
		s.observer.Operation(ctx, "put_current", outcome)
	}
	return results
}
