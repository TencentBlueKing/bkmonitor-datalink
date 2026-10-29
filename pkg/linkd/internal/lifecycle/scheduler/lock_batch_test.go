// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package scheduler

import (
	"context"
	"errors"
	"fmt"
	"testing"

	redis "github.com/redis/go-redis/v9"
)

type leasePipelineHook struct {
	redis.Hook
	rows                map[string]string
	pipelines, commands int
	loseResponse        bool
}

func (h *leasePipelineHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h *leasePipelineHook) ProcessHook(redis.ProcessHook) redis.ProcessHook {
	return func(context.Context, redis.Cmder) error { return errors.New("unexpected single lease request") }
}

func (h *leasePipelineHook) ProcessPipelineHook(redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		h.pipelines++
		h.commands += len(cmds)
		for _, c := range cmds {
			if ctx.Err() != nil {
				c.SetErr(ctx.Err())
				continue
			}
			a := c.Args()
			switch cmd := c.(type) {
			case *redis.BoolCmd:
				key, token := fmt.Sprint(a[1]), fmt.Sprint(a[2])
				_, busy := h.rows[key]
				if !busy {
					h.rows[key] = token
				}
				cmd.SetVal(!busy)
			case *redis.Cmd:
				key, token := fmt.Sprint(a[3]), fmt.Sprint(a[4])
				n := int64(0)
				if h.rows[key] == token {
					delete(h.rows, key)
					n = 1
				}
				cmd.SetVal(n)
			}
			if h.loseResponse {
				c.SetErr(errors.New("pipeline response lost"))
			}
		}
		return ctx.Err()
	}
}

func TestLeaseBatchPipelinesBusyCancellationAndUncertainAcquisition(t *testing.T) {
	for _, scenario := range []string{"normal", "busy", "lost_response", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			h := &leasePipelineHook{rows: map[string]string{}}
			client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
			client.AddHook(h)
			t.Cleanup(func() { _ = client.Close() })
			cfg := DefaultConfig()
			if scenario == "busy" {
				h.rows[cfg.LockKeyPrefix+":two"] = "other"
			}
			h.loseResponse = scenario == "lost_response"
			if scenario == "cancelled" {
				cancel()
			}
			locker := newRedisLocker(client, cfg, func() (string, error) { return "owned-token", nil })
			leases, errs := locker.AcquireBatch(ctx, []string{"one", "two", "three"})
			if h.pipelines != 1 || h.commands != 3 {
				t.Fatal(h.pipelines, h.commands)
			}
			for i, err := range errs {
				switch scenario {
				case "normal":
					if err != nil {
						t.Fatal(err)
					}
				case "busy":
					if i == 1 && !errors.Is(err, ErrLockBusy) {
						t.Fatal(err)
					}
				case "lost_response":
					if err == nil {
						t.Fatal("uncertain acquisition accepted")
					}
				case "cancelled":
					if !errors.Is(err, context.Canceled) {
						t.Fatal(err)
					}
				}
			}
			h.loseResponse = false
			released := locker.ReleaseBatch(context.WithoutCancel(ctx), leases)
			if h.pipelines != 2 || h.commands != 6 {
				t.Fatal(h.pipelines, h.commands)
			}
			if scenario == "normal" || scenario == "lost_response" {
				for _, err := range released {
					if err != nil {
						t.Fatal(err)
					}
				}
				if len(h.rows) != 0 {
					t.Fatal("owned lease leaked")
				}
			}
			if scenario == "busy" && (len(h.rows) != 1 || h.rows[cfg.LockKeyPrefix+":two"] != "other") {
				t.Fatal("deleted another owner")
			}
		})
	}
}
