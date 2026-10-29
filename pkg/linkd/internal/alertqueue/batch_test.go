// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package alertqueue

import (
	"context"
	"errors"
	"fmt"
	"testing"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/domain"
)

type publisherPipelineHook struct{ pipelines, commands int }

func (h *publisherPipelineHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h *publisherPipelineHook) ProcessHook(redis.ProcessHook) redis.ProcessHook {
	return func(context.Context, redis.Cmder) error { return errors.New("unexpected individual notification") }
}

func (h *publisherPipelineHook) ProcessPipelineHook(redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		h.pipelines++
		h.commands += len(cmds)
		for i, c := range cmds {
			if ctx.Err() != nil {
				c.SetErr(ctx.Err())
			} else {
				c.(*redis.StringCmd).SetVal(fmt.Sprintf("%d-0", i+1))
			}
		}
		return ctx.Err()
	}
}

func TestPublisher5000UsesOnePipelineAndPropagatesCancellation(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelled), func(t *testing.T) {
			client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
			t.Cleanup(func() { _ = client.Close() })
			h := &publisherPipelineHook{}
			client.AddHook(h)
			p := Publisher{Client: client, Stream: Stream("batch")}
			changes := make([]domain.AlertChange, 5000)
			for i := range changes {
				changes[i] = domain.AlertChange{BKTenantID: "tenant", AlertID: fmt.Sprint(i)}
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if cancelled {
				cancel()
			}
			for _, err := range p.PublishBatch(ctx, changes) {
				if cancelled {
					if !errors.Is(err, context.Canceled) {
						t.Fatal(err)
					}
				} else if err != nil {
					t.Fatal(err)
				}
			}
			if h.pipelines != 1 || h.commands != 5000 {
				t.Fatal(h.pipelines, h.commands)
			}
		})
	}
}
