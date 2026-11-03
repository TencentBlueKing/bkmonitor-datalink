// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package obevidence

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// An operator's store read is bounded only so that one request cannot take
// the process's memory: a document of a megabyte is read whole, and a read
// of a hundred keys (three hundred commands, a handful next to the
// business's commands a second) runs.
func TestAnOperatorsStoreReadIsBoundedGenerously(t *testing.T) {
	client := redisForTest(t)
	ctx := context.Background()
	service := New(Options{SourceStrategy: binding(client, "strategy_cache", "source")})
	document := `{"id":7,"name":"` + strings.Repeat("a", 1<<20) + `"}`
	if err := client.Set(ctx, "source.strategy_7", document, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if r := service.Store(ctx, StoreRequest{Family: FamilySourceStrategy, StrategyID: "7"}); r.Status != "ok" || !r.Complete {
		t.Fatalf("a 1 MiB document: status %s complete %v, want it read whole", r.Status, r.Complete)
	}
	keys := make([]string, 100)
	for i := range keys {
		keys[i] = fmt.Sprintf("source.many_%d", i)
		if err := client.Set(ctx, keys[i], `{"id":1}`, 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
	results, _ := readMany(ctx, "strategy_cache", binding(client, "strategy_cache", "source"), keys)
	for i, r := range results {
		if r.Status != "ok" {
			t.Fatalf("key %d of 100: status %s, want read", i, r.Status)
		}
	}
}

// What a store read still refuses says which bound it met, in its own code.
func TestAStoreReadPastItsBoundsSaysWhich(t *testing.T) {
	client := redisForTest(t)
	ctx := context.Background()
	keys := make([]string, MaxCommands/3+1)
	for i := range keys {
		keys[i] = fmt.Sprintf("source.k_%d", i)
	}
	results, _ := readMany(ctx, "strategy_cache", binding(client, "strategy_cache", "source"), keys)
	if results[0].Status != "too_many_keys" {
		t.Fatalf("past the command bound: status %s, want too_many_keys", results[0].Status)
	}
	service := New(Options{SourceStrategy: binding(client, "strategy_cache", "source")})
	if err := client.Set(ctx, "source.strategy_8", strings.Repeat("x", MaxDocumentBytes+10), 0).Err(); err != nil {
		t.Fatal(err)
	}
	if r := service.Store(ctx, StoreRequest{Family: FamilySourceStrategy, StrategyID: "8"}); r.Status != "document_too_large" {
		t.Fatalf("past the document bound: status %s, want document_too_large", r.Status)
	}
}
