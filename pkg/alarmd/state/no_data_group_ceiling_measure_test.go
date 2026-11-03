// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package state

import (
	"context"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// TestMeasureTheNoDataGroupCeiling measures, on a real server, what one
// Plan's memory write costs at the default bound and at the ceiling: the
// whole-record replace a fresh record takes, the longest single command the
// server ran for it (the time it blocked), what the write allocated and what
// the record holds in Redis. It is a measurement, run on demand with
// ALARMD_MEASURE_NO_DATA_CEILING=1, and asserts nothing.
func TestMeasureTheNoDataGroupCeiling(t *testing.T) {
	if os.Getenv("ALARMD_MEASURE_NO_DATA_CEILING") == "" {
		t.Skip("a measurement: set ALARMD_MEASURE_NO_DATA_CEILING=1")
	}
	server := startNoDataServer(t)
	ctx := context.Background()
	for _, groups := range []int{50000, 100000, 200000, 1000000} {
		if err := server.client.FlushAll(ctx).Err(); err != nil {
			t.Fatal(err)
		}
		store := server.store(t, func(options *ExecutionStoreOptions) { options.MaxNoDataGroups = groups })
		_ = server.client.ConfigSet(ctx, "slowlog-log-slower-than", "0").Err()
		_ = server.client.ConfigSet(ctx, "slowlog-max-len", "100000").Err()
		_ = server.client.Do(ctx, "SLOWLOG", "RESET").Err()
		before := usedMemory(t, server)
		var stats runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&stats)
		allocated := stats.TotalAlloc
		started := time.Now()
		mutation := firstNoDataWrite(t, 0, presentHostGroups(groups)...)
		built := time.Since(started)
		got := storeNoData(t, store, mutation)
		took := time.Since(started)
		runtime.ReadMemStats(&stats)
		if got.Status != execution.NoDataApplied {
			t.Fatalf("%d groups: %+v", groups, got)
		}
		entries, err := server.client.SlowLogGet(ctx, 100000).Result()
		if err != nil {
			t.Fatal(err)
		}
		var longest time.Duration
		var name string
		commands, over := 0, 0
		var shapes []string
		for _, entry := range entries {
			if len(entry.Args) == 0 || strings.EqualFold(entry.Args[0], "slowlog") || strings.EqualFold(entry.Args[0], "config") {
				continue
			}
			commands++
			if entry.Duration > 100*time.Millisecond {
				over++
			}
			if entry.Duration > longest {
				longest, name = entry.Duration, entry.Args[0]
			}
			if entry.Duration > 20*time.Millisecond && len(shapes) < 6 {
				shapes = append(shapes, entry.Args[0]+"/"+strconv.Itoa(len(entry.Args))+"args/"+entry.Duration.Round(time.Millisecond).String())
			}
		}
		t.Logf("%d groups: commands over 100ms %d; slow ones %v", groups, over, shapes)
		t.Logf("%d groups: build %s, build+write %s, %d server commands, longest %s (%s), Go allocated %d MiB, Redis +%d MiB",
			groups, built.Round(time.Millisecond), took.Round(time.Millisecond), commands, longest, name,
			(stats.TotalAlloc-allocated)>>20, (usedMemory(t, server)-before)>>20)
	}
}

func usedMemory(t *testing.T, server *noDataServer) uint64 {
	t.Helper()
	info, err := server.client.Info(context.Background(), "memory").Result()
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(info, "\n") {
		if value, found := strings.CutPrefix(strings.TrimSpace(line), "used_memory:"); found {
			used, _ := strconv.ParseUint(value, 10, 64)
			return used
		}
	}
	return 0
}
