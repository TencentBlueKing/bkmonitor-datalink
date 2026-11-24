// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package redisstate

import (
	"context"
	"errors"
	"fmt"
	"testing"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/domain"
	"linkd/internal/suppressioncleanup"
)

type terminalPipelineHook struct {
	pipelines, commands int
	eval, evalsha       int
	windows             bool
	partial             bool
}

func (h *terminalPipelineHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h *terminalPipelineHook) ProcessHook(redis.ProcessHook) redis.ProcessHook {
	return func(context.Context, redis.Cmder) error { return errors.New("unexpected individual terminal request") }
}

func (h *terminalPipelineHook) ProcessPipelineHook(redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		h.pipelines++
		h.commands += len(cmds)
		for i, c := range cmds {
			switch c.Name() {
			case "eval":
				h.eval++
			case "evalsha":
				h.evalsha++
			}
			if ctx.Err() != nil {
				c.SetErr(ctx.Err())
				continue
			}
			if h.partial && i == 0 {
				c.SetErr(errors.New("one clear unavailable"))
				continue
			}
			switch cmd := c.(type) {
			case *redis.IntCmd:
				cmd.SetVal(0)
			case *redis.Cmd:
				if h.windows {
					cmd.SetVal([]any{"ok"})
				} else {
					cmd.SetVal(int64(0))
				}
			}
		}
		return ctx.Err()
	}
}

func TestTerminal5000UsesPhasePipelinesAndKeepsUnknownDistinct(t *testing.T) {
	for _, scenario := range []string{"normal", "partial", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", ContextTimeoutEnabled: true})
			t.Cleanup(func() { _ = client.Close() })
			h := &terminalPipelineHook{windows: true, partial: scenario == "partial"}
			client.AddHook(h)
			s, err := New(client, "batch-terminal")
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if scenario == "cancelled" {
				cancel()
			}
			causes := make([]suppressioncleanup.Cause, 5000)
			alerts := make([]domain.Alert, 5000)
			for i := range causes {
				id := fmt.Sprintf("alert-%04d", i)
				causes[i] = suppressioncleanup.Cause{TenantID: "tenant", SourceID: "source", Fingerprint: id, Trigger: "alert_terminal", AlertID: id, Revision: 2, Status: domain.AlertStatusClosed}
				alerts[i] = domain.Alert{BKTenantID: "tenant", AlertID: id}
			}
			results, errs := s.ClearSuppressionBatch(ctx, causes)
			if h.eval != 2 || h.evalsha != 9998 {
				t.Fatal("script bodies repeated per member", h.eval, h.evalsha)
			}
			if h.pipelines != 1 || h.commands != 10000 {
				t.Fatal(h.pipelines, h.commands)
			}
			for i, r := range results {
				if scenario == "cancelled" || scenario == "partial" && i == 0 {
					if errs[i] == nil || r.Clip.State != "unavailable" || r.Clip.Removed != nil {
						t.Fatal("unknown became zero", i, r, errs[i])
					}
				} else if errs[i] != nil || r.Clip.State != "confirmed" || r.Clip.Removed == nil || *r.Clip.Removed != 0 {
					t.Fatal(i, r, errs[i])
				}
			}
			if scenario == "normal" {
				h.windows = false
				for _, err := range s.ClearDependencyOwnersBatch(ctx, alerts) {
					if err != nil {
						t.Fatal(err)
					}
				}
				_, errs = s.PublishShieldHintsBatch(ctx, alerts)
				for _, err := range errs {
					if err != nil {
						t.Fatal(err)
					}
				}
				if h.pipelines != 3 || h.commands != 20000 {
					t.Fatal(h.pipelines, h.commands)
				}
			}
		})
	}
}
