// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package obchannel

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/redistest"
)

// A receipt is one script on a real server, each way a script call meets
// one: it lands newest first, the list cut to its bound, and its life
// renewed to the retention in seconds; a refused write is reported as not
// landed and leaves nothing.
func TestASREReceiptIsOneScript(t *testing.T) {
	const key = "test.sre:{env}:receipts"
	action := SREAction{Op: "pod.delete", RequestID: "newest", At: time.Unix(1_700_000_000, 0).UTC()}
	redistest.CheckScriptSite(t, redistest.ScriptSite{
		// A full list, so the trim has one to cut.
		Prepare: func(t *testing.T, client *redis.Client) {
			for i := 0; i < MaxSREReceipts; i++ {
				if err := client.LPush(context.Background(), key, `{"op":"older"}`).Err(); err != nil {
					t.Fatal(err)
				}
			}
		},
		Call: func(client *redis.Client) error {
			return NewSREReceipts(client, key, nil, nil).record(action)
		},
		Written: func(t *testing.T, client *redis.Client) {
			ctx := context.Background()
			entries := client.LRange(ctx, key, 0, -1).Val()
			var newest SREAction
			if len(entries) != MaxSREReceipts || json.Unmarshal([]byte(entries[0]), &newest) != nil || newest.RequestID != "newest" {
				t.Fatalf("%d entries, newest %q; want %d with the new one first", len(entries), entries[0], MaxSREReceipts)
			}
			if ttl := client.TTL(ctx, key).Val(); ttl <= SREReceiptRetention-time.Minute || ttl > SREReceiptRetention {
				t.Fatalf("ttl %v, want the retention %v", ttl, SREReceiptRetention)
			}
		},
		Refused: func(t *testing.T, client *redis.Client, _ error) {
			var outcomes []bool
			NewSREReceipts(client, key, nil, func(ok bool) { outcomes = append(outcomes, ok) }).Record(action)
			if len(outcomes) != 1 || outcomes[0] {
				t.Fatalf("a refused write reported %v, want one not landed", outcomes)
			}
		},
	})
}
