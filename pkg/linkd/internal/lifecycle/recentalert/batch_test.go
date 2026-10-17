// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package recentalert

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/domain"
	"linkd/internal/store"
)

type cachePipelineFixture struct {
	*fakeRedis
	pipelines    int
	failAt       int
	loseResponse bool
}

type cachePipeline struct {
	redis.Pipeliner
	owner    *cachePipelineFixture
	calls    []func()
	commands []redis.Cmder
}

func (p *cachePipeline) Set(ctx context.Context, key string, value any, ttl time.Duration) *redis.StatusCmd {
	cmd := redis.NewStatusCmd(ctx)
	i := len(p.commands)
	p.commands = append(p.commands, cmd)
	p.calls = append(p.calls, func() {
		if ctx.Err() != nil {
			cmd.SetErr(ctx.Err())
			return
		}
		if i == p.owner.failAt {
			cmd.SetErr(errors.New("set unavailable"))
			return
		}
		result := p.owner.Set(ctx, key, value, ttl)
		cmd.SetVal(result.Val())
		cmd.SetErr(result.Err())
	})
	return cmd
}

func (f *cachePipelineFixture) Pipelined(ctx context.Context, fn func(redis.Pipeliner) error) ([]redis.Cmder, error) {
	f.pipelines++
	p := &cachePipeline{owner: f}
	if err := fn(p); err != nil {
		return p.commands, err
	}
	if f.loseResponse {
		return p.commands, errors.New("response lost")
	}
	var first error
	for i, run := range p.calls {
		run()
		if first == nil {
			first = p.commands[i].Err()
		}
	}
	return p.commands, first
}

func TestCurrentCacheExplicitPipelineAndFailureRetry(t *testing.T) {
	for _, fault := range []string{"none", "partial", "response lost", "canceled"} {
		t.Run(fault, func(t *testing.T) {
			f := &cachePipelineFixture{fakeRedis: newFakeRedis(), failAt: -1}
			if fault == "partial" {
				f.failAt = 1
			}
			f.loseResponse = fault == "response lost"
			cache, err := newStore(f, f.transaction, Config{KeyPrefix: "source-a", RefreshInterval: 5 * time.Second}, nil)
			if err != nil {
				t.Fatal(err)
			}
			first := cachedAlert("one", domain.AlertStatusActive, "")
			second := first
			second.Alert = first.Alert.Clone()
			second.Alert.Fingerprint = "different"
			current := []store.StoredAlert{first, second}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if fault == "canceled" {
				cancel()
			}
			result := cache.PutCurrentSnapshots(ctx, current)
			if f.pipelines != 1 {
				t.Fatal("not one explicit pipeline")
			}
			for i, err := range result {
				wantFailure := fault == "response lost" || fault == "canceled" || (fault == "partial" && i == 1)
				if (err != nil) != wantFailure {
					t.Fatal("independent failure", i, err)
				}
			}
			f.failAt = -1
			f.loseResponse = false
			for _, err := range cache.PutCurrentSnapshots(t.Context(), current) {
				if err != nil {
					t.Fatal(err)
				}
			}
			if len(f.values) != 2 {
				t.Fatal("lost cache keys")
			}
			for _, item := range current {
				key, _ := cache.currentKey(activeKey(item.Alert))
				want, _ := encodeSnapshot(item)
				if !reflect.DeepEqual(f.values[key], want) || f.expirations[key] != 10*time.Second {
					t.Fatal("cache token, key or TTL changed")
				}
			}
		})
	}
}
