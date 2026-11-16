// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"runtime/metrics"
	"runtime/pprof"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// releasedResidueBound is what the live heap may stand above where it stood
// before the Slot once the Slot has given its reservation back: the State
// cache trading the second Slot's entries for the third's, and the noise of a
// heap of a few hundred MiB. A Slot's own structures are hundreds of MiB.
const releasedResidueBound = 64 << 20

// releasedReadOffsets are when, after the pool reads empty, the heap is
// collected and read: at once, and at the three seconds a production refresh
// has landed after a release and still read the Slot's structures live.
var releasedReadOffsets = []time.Duration{0, time.Second, 3 * time.Second, 10 * time.Second}

// gcLiveHeap is the live heap the collection just forced marked, the number a
// production heap reading publishes.
func gcLiveHeap() uint64 {
	runtime.GC()
	sample := []metrics.Sample{{Name: "/gc/heap/live:bytes"}}
	metrics.Read(sample)
	return sample[0].Value.Uint64()
}

type releasedRead struct {
	offset   time.Duration
	at       time.Duration
	live     uint64
	reserved uint64
}

// TestALargeQueryGroupsSlotLeavesNothingLiveOnceItsReservationIsBack runs
// three Slots of the largest Query Group seen on a 4 GiB replica through the
// production bundle on a real Redis, and once the third has given its
// reservation back - the pool reading empty - collects and reads the live
// heap at once and a second, three and ten seconds on. Each must stand
// within releasedResidueBound of the live heap before the Slot: a Slot's
// structures reachable with nothing reserved for them is memory the pool
// does not see. Set ALARMD_MEASURE_PROFILE_DIR to keep the heap profile of
// the first read. Runs on demand with ALARMD_MEASURE_LARGE_GROUP=1.
func TestALargeQueryGroupsSlotLeavesNothingLiveOnceItsReservationIsBack(t *testing.T) {
	if os.Getenv("ALARMD_MEASURE_LARGE_GROUP") == "" {
		t.Skip("a measurement: set ALARMD_MEASURE_LARGE_GROUP=1")
	}
	const series = 95_760
	address, redisClient := startPhaseTwoRedis(t)
	ctx := context.Background()
	if err := redisClient.Set(ctx, "alarm-config.strategy_ids", `[1001]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := redisClient.Set(ctx, "alarm-config.strategy_1001", largeGroupStrategy(t), 0).Err(); err != nil {
		t.Fatal(err)
	}
	base := time.Now().Unix()
	base = base - base%60 + 120
	var clock atomic.Int64
	clock.Store(base)
	now := func() time.Time { return time.Unix(clock.Load(), 0) }
	var answersMu sync.Mutex
	answers := map[int64][]byte{}
	uqServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var query struct {
			StartTime string `json:"start_time"`
		}
		if err := json.NewDecoder(request.Body).Decode(&query); err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		var start int64
		if _, err := fmt.Sscan(query.StartTime, &start); err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		answersMu.Lock()
		answer, ok := answers[start]
		if !ok {
			answer = largeGroupAnswer(series, start*1000)
			answers[start] = answer
		}
		answersMu.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write(answer)
	}))
	defer uqServer.Close()
	cfg := largeGroupConfig(t, address, uqServer.URL)
	var completed atomic.Int64
	observer := observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
		if observation.Stage == observability.StageSlotCompleted {
			completed.Add(1)
		}
	})
	recorder := metric.NewRecorder(metric.BuildInfo{})
	bundle, err := openProductionPhaseTwoBundleWithDependencies(
		ctx, cfg, recorder, observability.Discard(observability.ComponentRuntime), newPhaseTwoApplicationHealth(),
		func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
			return controlplane.NewLegacyRedisStrategySource(client, prefix)
		},
		phaseTwoProductionExternalDependencies{
			Now: now, HTTPClient: uqServer.Client(), AdditionalObserver: observer,
			PrepareEvents: preparedEvents(func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) {
				return &recordingPhaseTwoEventSink{}, nil
			}),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := bundle.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = bundle.Shutdown(ctx) }()
	settleExecutableView(ctx, bundle, 10*time.Second)

	const slots = 3
	for slot := int64(0); slot < slots-1; slot++ {
		clock.Store(base + slot*60 + 1)
		if err := bundle.runScheduledOnce(ctx); err != nil {
			t.Fatalf("Slot %d: %v", slot, err)
		}
	}
	if completed.Load() != slots-1 {
		t.Fatalf("%d Slots completed before the measured one, want %d", completed.Load(), slots-1)
	}
	before := gcLiveHeap()

	// The pool read every five milliseconds while the Slot runs. Once it has
	// held at least the Slot's input and then reads empty, the Slot has given
	// its reservation back, whether or not the call that ran it has returned.
	var reads []releasedRead
	var peakReserved atomic.Uint64
	var returnedAt atomic.Int64
	returnedAt.Store(-1)
	done := make(chan struct{})
	measured := make(chan struct{})
	started := time.Now()
	go func() {
		defer close(measured)
		ticker := time.NewTicker(5 * time.Millisecond) // fixed-wait: the sampling interval of a measurement; it waits on no condition
		defer ticker.Stop()
		for {
			reserved, _ := retainedReservedOf(recorder)
			if reserved > peakReserved.Load() {
				peakReserved.Store(reserved)
			}
			if reserved == 0 && peakReserved.Load() >= 64<<20 {
				break
			}
			select {
			case <-done:
				return
			case <-ticker.C:
			}
		}
		emptied := time.Now()
		for index, offset := range releasedReadOffsets { // fixed-wait: each read is taken at its set offset after the release, the quantity measured
			if wait := time.Until(emptied.Add(offset)); wait > 0 {
				time.Sleep(wait) // fixed-wait: the read is taken at a set time after the release, the quantity measured
			}
			live := gcLiveHeap()
			reserved, _ := retainedReservedOf(recorder)
			reads = append(reads, releasedRead{offset: offset, at: time.Since(started), live: live, reserved: reserved})
			if dir := os.Getenv("ALARMD_MEASURE_PROFILE_DIR"); index == 0 && dir != "" {
				file, err := os.Create(filepath.Join(dir, "released-heap.pb.gz"))
				if err == nil {
					_ = pprof.Lookup("heap").WriteTo(file, 0)
					_ = file.Close()
				}
			}
		}
	}()
	clock.Store(base + (slots-1)*60 + 1)
	runErr := bundle.runScheduledOnce(ctx)
	returnedAt.Store(int64(time.Since(started)))
	if runErr != nil {
		close(done)
		<-measured
		t.Fatal(runErr)
	}
	select {
	case <-measured:
	case <-time.After(releasedReadOffsets[len(releasedReadOffsets)-1] + time.Minute): // fixed-wait: a bound on a pool that never reads empty, which the length check below then reports
		close(done)
		<-measured
	}
	if completed.Load() != slots {
		t.Fatalf("%d Slots completed, want %d", completed.Load(), slots)
	}
	if len(reads) != len(releasedReadOffsets) {
		t.Fatalf("the pool never read empty after holding the Slot (peak %d reserved); %d reads", peakReserved.Load(), len(reads))
	}
	t.Logf("series=%d live_before=%d peak_reserved=%d returned_at=%s", series, before, peakReserved.Load(),
		time.Duration(returnedAt.Load()).Round(time.Millisecond))
	for _, read := range reads {
		residue := int64(read.live) - int64(before)
		t.Logf("released+%s (at %s): live %d reserved %d residue %d", read.offset, read.at.Round(time.Millisecond),
			read.live, read.reserved, residue)
		if residue > releasedResidueBound && read.reserved < uint64(residue) {
			t.Errorf("released+%s: %d bytes live above before with %d reserved, want at most %d unreserved",
				read.offset, residue, read.reserved, releasedResidueBound)
		}
	}
}
