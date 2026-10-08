// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package redisstate

import (
	"context"
	"fmt"
	"strconv"
	"time"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/domain"
	"linkd/internal/policy"
)

func (s *Store) statisticsKey(scope policy.Scope, at time.Time) string {
	return s.base(scope.TenantID) + ":observations:" + string(scope.Kind) + ":" + strconv.FormatInt(at.UTC().Truncate(time.Hour).Unix(), 10)
}

// RecordPolicyObservation 写入有界小时桶。调用者限制并发和超时；失败不影响策略业务结果。
// 每租户/类别/小时最多4096个策略，25小时TTL；指标是尽力采样，不是持久审计或唯一Event统计。
func (s *Store) RecordPolicyObservation(ctx context.Context, scope policy.Scope, id, outcome string, at time.Time) error {
	if scope.Validate() != nil || domain.ValidateIdentityPart("policy", id, 80) != nil || at.IsZero() {
		return policy.ErrInvalid
	}
	switch outcome {
	case "matched", "not_matched", "unavailable", "execution_skipped":
	default:
		return policy.ErrInvalid
	}
	n, err := recordObservationScript.Run(ctx, s.client, []string{s.statisticsKey(scope, at)}, id, outcome).Int()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrBudget
	}
	return nil
}

var recordObservationScript = redis.NewScript(`
local kind=redis.call('TYPE',KEYS[1]).ok
if kind~='none' and kind~='hash' then return -1 end
local marker=ARGV[1]..':seen'
if redis.call('HEXISTS',KEYS[1],marker)==0 then
 if redis.call('HLEN',KEYS[1])>=20480 then return 0 end
 redis.call('HSET',KEYS[1],marker,1,ARGV[1]..':matched',0,ARGV[1]..':not_matched',0,ARGV[1]..':unavailable',0,ARGV[1]..':execution_skipped',0)
end
redis.call('HINCRBY',KEYS[1],ARGV[1]..':'..ARGV[2],1)
redis.call('EXPIRE',KEYS[1],90000)
return 1
`)

// PolicyStatistics 只读取明确身份和固定小时桶；读取失败返回错误，不把不可用显示为零。
func (s *Store) PolicyStatistics(ctx context.Context, q policy.StatisticsQuery, at time.Time) (policy.StatisticsPage, error) {
	select {
	case s.statisticsSlots <- struct{}{}:
		defer func() { <-s.statisticsSlots }()
	default:
		return policy.StatisticsPage{}, policy.ErrPreviewCapacity
	}
	if err := q.Validate(); err != nil {
		return policy.StatisticsPage{}, err
	}
	if at.IsZero() {
		return policy.StatisticsPage{}, policy.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	from := at.UTC().Truncate(time.Hour).Add(-time.Duration(q.Hours-1) * time.Hour)
	out := policy.StatisticsPage{Scope: q.Scope, From: from, To: at.UTC(), Hours: q.Hours, Mode: "execution_observations", Items: make([]policy.PolicyStatistics, len(q.IDs))}
	fields := []string{}
	for i, id := range q.IDs {
		out.Items[i].ID = id
		for _, kind := range []string{"matched", "not_matched", "unavailable", "execution_skipped"} {
			fields = append(fields, id+":"+kind)
		}
	}
	pipe := s.client.Pipeline()
	commands := make([]*redis.SliceCmd, 0, q.Hours)
	for i := range q.Hours {
		commands = append(commands, pipe.HMGet(ctx, s.statisticsKey(q.Scope, from.Add(time.Duration(i)*time.Hour)), fields...))
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return policy.StatisticsPage{}, err
	}
	for _, command := range commands {
		values, err := command.Result()
		if err != nil || len(values) != len(fields) {
			return policy.StatisticsPage{}, ErrState
		}
		for i, raw := range values {
			if raw == nil {
				continue
			}
			value, ok := raw.(string)
			if !ok {
				return policy.StatisticsPage{}, ErrState
			}
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil || n < 0 || n > 1<<50 {
				return policy.StatisticsPage{}, fmt.Errorf("invalid observation count")
			}
			row := &out.Items[i/4]
			switch i % 4 {
			case 0:
				row.Matched += n
			case 1:
				row.NotMatched += n
			case 2:
				row.Unavailable += n
			case 3:
				row.ExecutionSkipped += n
			}
		}
	}
	return out, nil
}
