// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"runtime/debug"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/redistest"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/memoryline"
)

// withFullLine runs fn with the process's soft memory limit below its live
// heap, so the observation memory line has no room for anything.
func withFullLine(t *testing.T, fn func(line *memoryline.Line)) {
	t.Helper()
	previous := debug.SetMemoryLimit(1)
	defer debug.SetMemoryLimit(previous)
	line := memoryline.New()
	defer line.Close()
	fn(line)
}

// A fleet view an operator asks for is read even with the memory line full,
// and is reported over the line rather than deferred.
func TestAnOperatorsFleetViewIsReadWithTheLineFull(t *testing.T) {
	client := redistest.Start(t).Client()
	store, err := fleet.NewRedisStore(client, "operator-reads", time.Minute, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Publish(context.Background(), fleet.Snapshot{Replica: "pod-a", TakenAt: time.Now(), Owned: 1}); err != nil {
		t.Fatal(err)
	}
	withFullLine(t, func(line *memoryline.Line) {
		operatorFleetLoads(store, line)
		snapshots, err := store.Load(context.Background(), []string{"pod-a"})
		if err != nil || len(snapshots) != 1 {
			t.Fatalf("loaded %d snapshots, error %v; want the view read", len(snapshots), err)
		}
		if reading := line.Read(); reading.OverTotal[memoryline.ConsumerFleetView] == 0 || reading.RefusedTotal[memoryline.ConsumerFleetView] != 0 {
			t.Fatalf("over %d, refused %d; want the read reported over the line, not refused",
				reading.OverTotal[memoryline.ConsumerFleetView], reading.RefusedTotal[memoryline.ConsumerFleetView])
		}
	})
}

// A diagnosis page's Progress reads go ahead with the line full.
func TestADiagnosisPagesProgressReadsGoAheadWithTheLineFull(t *testing.T) {
	withFullLine(t, func(line *memoryline.Line) {
		if !operatorProgressReads(line)(1 << 20) {
			t.Fatal("a diagnosis page's Progress read was refused with the line full")
		}
		if line.Read().OverTotal[memoryline.ConsumerDiagnosisProgress] != 1 {
			t.Fatal("the read past the line was not reported")
		}
	})
}
