// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package openalerts

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-redis/redis/v8"
)

// Publication is one read of the publisher's keys for the strategies asked.
//
// The heartbeat and the sets are reported separately and neither is
// defaulted: a missing heartbeat is nil, an unreadable one is an error kept
// beside it, and a strategy whose key is absent is absent from Sets. What
// each of those means is the cache's decision, not the source's.
type Publication struct {
	Heartbeat    *Heartbeat
	HeartbeatErr error
	Sets         map[StrategyKey][]string
}

// Source reads the publisher's keys. A non-nil error is a read that did not
// happen (transport), which the cache treats as an unavailable publication;
// a read that happened and found nothing is a Publication with a nil
// Heartbeat.
type Source interface {
	Read(ctx context.Context, keys []StrategyKey) (Publication, error)
}

// RedisSource reads the contract keys from the control plane Redis.
type RedisSource struct {
	client redis.Cmdable
	// batch bounds one pipeline; a worker's strategies are read in chunks
	// so that one round trip never carries an unbounded command count.
	batch int
}

const defaultReadBatch = 256

func NewRedisSource(client redis.Cmdable) (*RedisSource, error) {
	if client == nil {
		return nil, errors.New("alarmd openalerts: a redis client is required")
	}
	return &RedisSource{client: client, batch: defaultReadBatch}, nil
}

func (source *RedisSource) Read(ctx context.Context, keys []StrategyKey) (Publication, error) {
	if source == nil || source.client == nil {
		return Publication{}, errors.New("alarmd openalerts: source is not initialised")
	}
	publication := Publication{Sets: make(map[StrategyKey][]string, len(keys))}
	fields, err := source.client.HGetAll(ctx, HeartbeatKey).Result()
	if err != nil {
		return Publication{}, fmt.Errorf("alarmd openalerts: read heartbeat: %w", err)
	}
	// HGETALL of a missing key is an empty hash, which is the one shape that
	// must not be parsed: it would fail on the first field and read as an
	// unreadable heartbeat rather than a missing one.
	if len(fields) > 0 {
		heartbeat, parseErr := ParseHeartbeat(fields)
		if parseErr != nil {
			publication.HeartbeatErr = parseErr
		} else {
			publication.Heartbeat = &heartbeat
		}
	}
	// Without a readable heartbeat the sets are not the consumer's word and
	// the cache will not use them, so they are not read: on a deployment
	// where the publisher does not exist yet this is the difference between
	// one command per cycle and one per tracked strategy.
	if publication.Heartbeat == nil {
		return publication, nil
	}
	for start := 0; start < len(keys); start += source.batch {
		end := start + source.batch
		if end > len(keys) {
			end = len(keys)
		}
		chunk := keys[start:end]
		pipe := source.client.Pipeline()
		commands := make([]*redis.StringSliceCmd, len(chunk))
		for index, key := range chunk {
			commands[index] = pipe.SMembers(ctx, SetKey(key))
		}
		if _, err := pipe.Exec(ctx); err != nil {
			return Publication{}, fmt.Errorf("alarmd openalerts: read sets: %w", err)
		}
		for index, command := range commands {
			members, err := command.Result()
			if err != nil {
				return Publication{}, fmt.Errorf("alarmd openalerts: read set %s: %w", SetKey(chunk[index]), err)
			}
			// A Redis SET with no members does not exist, so empty and absent
			// are the same observation here; the cache reads both as "not
			// written this cycle", which the publisher's TTL makes true.
			if len(members) > 0 {
				publication.Sets[chunk[index]] = members
			}
		}
	}
	return publication, nil
}
