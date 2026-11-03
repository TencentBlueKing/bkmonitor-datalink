// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/go-redis/redis/v8"
)

// A Slot read is bounded so one request cannot take the process's memory,
// not by a count an investigation meets: a few hundred commands (a handful
// next to the business's thousands a second) and a document of several
// megabytes read.
func TestASlotReadIsBoundedGenerously(t *testing.T) {
	_, client := startPhaseTwoRedis(t)
	ctx := context.Background()
	if err := client.Set(ctx, "small", "v", 0).Err(); err != nil {
		t.Fatal(err)
	}
	r := newCLISlotRedis(client)
	for i := 0; i < 500; i++ {
		if err := r.Get(ctx, "small").Err(); err != nil {
			t.Fatalf("command %d: %v", i+1, err)
		}
	}
	client.Set(ctx, "large", strings.Repeat("x", 4<<20), 0)
	if err := newCLISlotRedis(client).Get(ctx, "large").Err(); err != nil {
		t.Fatalf("a 4 MiB document: %v", err)
	}
}

// The CLI's Redis clients serve an operator's reads, a handful next to the
// business pool: several at once do not queue on a pool of two.
func TestTheCLIRedisPoolDoesNotQueueAnOperatorsReads(t *testing.T) {
	address, _ := startPhaseTwoRedis(t)
	options := cliRedisOptions(config.RedisConnectionConfig{Mode: config.RedisModeStandalone, Address: address}, "auth", nil)
	if options.PoolSize < 16 || options.PoolTimeout < 5*time.Second {
		t.Fatalf("pool %d, pool timeout %s; want at least 16 and 5s", options.PoolSize, options.PoolTimeout)
	}
	client := redis.NewUniversalClient(options)
	defer client.Close()
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// A blocking command holds its connection for its whole wait.
			if err := client.BLPop(context.Background(), 300*time.Millisecond, "absent-list").Err(); err != nil && err != redis.Nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("a read waited out its pool: %v", err)
	}
}
