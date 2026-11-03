// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package ownership

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func queryGroupsForRead(ids ...string) []execution.QueryGroupIdentity {
	queryGroups := make([]execution.QueryGroupIdentity, 0, len(ids))
	for _, id := range ids {
		queryGroups = append(queryGroups, execution.QueryGroupIdentity(id))
	}
	return queryGroups
}

// A batched read that could not be performed returns an error and no records.
//
// This is the failure the batching had to preserve, and it is a store-level
// fact that no fake can stand in for: the scheduler's cases prove the round
// stops when the store returns an error, and say nothing about whether the
// store returns one. With the pipeline swallowing its error and handing back
// whatever replies it managed to decode, every one of those cases still
// passes -- the round would simply be told that no Query Group has a record,
// which is the input that rendezvouses the whole population onto new owners.
//
// A cancelled context is the shape that reaches this in production: the
// Control Leader losing its lease, or the process shutting down, cancels the
// round's context mid-read, and go-redis fails the command without dialing.
// That needs no server, so this runs everywhere rather than skipping on the
// machines that have no redis-server -- a skipped case cannot refuse a
// regression.
func TestABatchedControlReadThatFailsReturnsNoRecords(t *testing.T) {
	store, err := NewRedisStore(RedisStoreOptions{
		// Never dialled: the context is already cancelled when the calls are
		// made, so the address only has to be well formed.
		Address: "127.0.0.1:1", Prefix: "alarmd-ownership-cancelled", DialTimeout: time.Second,
		ReadTimeout: time.Second, WriteTimeout: time.Second, PoolSize: 1,
	})
	if err != nil {
		t.Fatalf("NewRedisStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	records, stats, err := store.ReadAssignments(ctx, queryGroupsForRead("query-group-1", "query-group-2", "query-group-3"))
	if err == nil {
		t.Fatal("ReadAssignments() on a cancelled context returned no error; a read that did not happen must not " +
			"be reported as a population with no Assignment records")
	}
	if records != nil {
		t.Fatalf("ReadAssignments() returned %d records beside its error; a partial map reads downstream as "+
			"'these Query Groups are unassigned', which is what republishes them", len(records))
	}
	// The stats still say a round trip was attempted. A failed read costs the
	// round its wait, and leaving it out of the reported total would take the
	// slowest rounds out of the distribution they matter most in.
	if stats.RoundTrips == 0 {
		t.Fatalf("a failed batch reported %d round trips, want the attempt it made", stats.RoundTrips)
	}

	workers, workerStats, err := store.ListReadyWorkers(ctx, time.Now())
	if err == nil {
		t.Fatal("ListReadyWorkers() on a cancelled context returned no error; a registry that could not be read " +
			"must not be reported as a fleet with no ready workers")
	}
	if workers != nil {
		t.Fatalf("ListReadyWorkers() returned %d workers beside its error; a short ready set does not read as a "+
			"failure anywhere downstream, it reads as workers having left", len(workers))
	}
	if workerStats.RoundTrips == 0 {
		t.Fatalf("a failed registry read reported %d round trips, want the attempt it made", workerStats.RoundTrips)
	}
}
