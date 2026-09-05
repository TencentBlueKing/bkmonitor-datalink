// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/access"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/state"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// This opt-in measurement accepts an external readback; private strategy data
// never enters source control. Only allocation and timing aggregates are logged.
// Timelines are reconstructed for measurement, not asserted to be live facts.
func TestPhaseTwoSnapshotPreparationProfile(t *testing.T) {
	path := os.Getenv("ALARMD_PREPARATION_FIXTURE")
	if path == "" {
		t.Skip("external Snapshot readback not configured")
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	zipped, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer zipped.Close()
	var readback struct {
		Keys map[string]struct {
			Value string `json:"value"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(zipped).Decode(&readback); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, client := startPhaseTwoRedis(t)
	var activation controlplane.ActivationState
	var prefix string
	for key, value := range readback.Keys {
		if err := client.Set(ctx, key, value.Value, 0).Err(); err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(key, ":activation") {
			prefix = strings.TrimSuffix(key, ":activation")
			if err := json.Unmarshal([]byte(value.Value), &activation); err != nil {
				t.Fatal(err)
			}
		}
	}
	if prefix == "" {
		t.Fatal("readback has no activation")
	}
	if err := client.Set(ctx, prefix+":snapshot_epoch:"+string(activation.Current.SnapshotRevision), activation.Current.PublicationEpoch, 0).Err(); err != nil {
		t.Fatal(err)
	}
	repository, err := controlplane.NewRedisCatalogRepository(client, prefix, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := repository.LoadSnapshot(ctx, activation.Current.SnapshotRevision)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	compiler, err := strategy.NewCompiler(strategy.NewDefaultAlgorithmCompilerRegistry(), cfg.CompilerLimits())
	if err != nil {
		t.Fatal(err)
	}
	sm, err := state.RuntimeStateSemantics()
	if err != nil {
		t.Fatal(err)
	}
	semantics := strategy.StateSemantics{StateSchemaVersion: sm.StateSchemaVersion, CodecSemanticsVersion: sm.CodecSemanticsVersion,
		IdentitySchemaDigest: sm.IdentitySchemaDigest, SourceTimeSemanticsVersion: sm.SourceTimeSemanticsVersion, HistoryCellSemanticsVersion: sm.HistoryCellSemanticsVersion}
	catalog, err := controlplane.NewRedisCatalogRuntime(repository, compiler, semantics, cfg.PhaseTwo.Access.DownstreamExecutionReserve.Duration())
	if err != nil {
		t.Fatal(err)
	}
	records := make(map[execution.PlanIdentity]controlplane.PlanActivationRecord)
	for _, record := range activation.Plans {
		records[record.Fact.Plan] = record
	}
	refs := make([]execution.FrozenExecutionContractRef, 0, len(snapshot.QueryGroups))
	for _, group := range snapshot.QueryGroups {
		schedule := execution.FrozenQueryGroupSchedule{Segment: execution.ScheduleSegmentFact{
			Publication: execution.SnapshotPublicationRef{SnapshotRevision: activation.Current.SnapshotRevision, PublicationEpoch: execution.PublicationEpoch(activation.Current.PublicationEpoch)},
			QueryGroup:  group.Identity, QueryRevision: group.QueryPlan.QueryRevision, ScheduleRevision: group.ScheduleRevision, Start: 1}}
		plans := make([]controlplane.PlanActivationRecord, 0, len(group.Plans))
		for _, plan := range group.Plans {
			schedule.Plans = append(schedule.Plans, execution.FrozenPlanSchedule{Identity: plan.Identity, ScheduleRevision: plan.ScheduleRevision, Spec: plan.ScheduleSpec})
			plans = append(plans, records[plan.Identity])
		}
		at, ok := schedule.NextSlotAfter(1_800_000_000)
		if !ok {
			t.Fatal("no measurement Slot")
		}
		timeline := struct {
			Schema     string                       `json:"schema_version"`
			Revision   uint64                       `json:"record_revision"`
			QueryGroup execution.QueryGroupIdentity `json:"query_group"`
			Segments   []any                        `json:"segments"`
		}{
			Schema: "alarmd-control-schedule-timeline-v1", Revision: 1, QueryGroup: group.Identity, Segments: []any{struct {
				Schedule execution.FrozenQueryGroupSchedule  `json:"schedule"`
				Plans    []controlplane.PlanActivationRecord `json:"plans"`
			}{schedule, plans}}}
		payload, err := json.Marshal(timeline)
		if err != nil {
			t.Fatal(err)
		}
		if err := client.Set(ctx, prefix+":schedule_timeline:"+string(group.Identity), payload, 0).Err(); err != nil {
			t.Fatal(err)
		}
		fact, err := catalog.FreezeSlotContract(ctx, execution.FreezeSlotContractRequest{QueryGroup: group.Identity, ScheduleRevision: group.ScheduleRevision, ScheduleSegmentStart: 1, EvaluationTime: at, DuePlans: schedule.DuePlanRefs(at)})
		if err != nil {
			t.Fatalf("prepare measurement contract: %v", err)
		}
		refs = append(refs, fact.Contract)
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].Slot.QueryGroup < refs[j].Slot.QueryGroup })
	resolver, err := newProductionFrozenExecution(catalog, repository, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("snapshot_qgs=%d gomaxprocs=%d; warm caches; reconstructed schedules; setup excluded; no HTTP/evaluation", len(refs), runtime.GOMAXPROCS(0))
	for _, fanout := range []int{2, 4, 8, 16} {
		t.Run(fmt.Sprintf("F%d", fanout), func(t *testing.T) {
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			peak := before.HeapAlloc
			previous := before
			var maxBatchAllocation, heapUpperBound uint64
			started := time.Now()
			for offset := 0; offset < len(refs); offset += fanout {
				count := min(fanout, len(refs)-offset)
				held := make([]access.PreparedExecution, count)
				errs := make([]error, count)
				var wg sync.WaitGroup
				for i := 0; i < count; i++ {
					wg.Add(1)
					go func(i int) {
						defer wg.Done()
						frozen, err := resolver.ResolveFrozenPlan(ctx, refs[offset+i])
						if err == nil {
							held[i], err = access.Prepare(refs[offset+i], frozen, cfg.PhaseTwo.Access.MinReadyDelay.Duration())
						}
						errs[i] = err
					}(i)
				}
				wg.Wait()
				for _, err := range errs {
					if err != nil {
						t.Fatal(err)
					}
				}
				var sample runtime.MemStats
				runtime.ReadMemStats(&sample)
				peak = max(peak, sample.HeapAlloc)
				// TotalAlloc includes objects already collected inside this batch.
				// Adding it to the starting heap gives a conservative tested-batch
				// heap bound, unlike the batch-end samples which can miss peaks.
				allocated := sample.TotalAlloc - previous.TotalAlloc
				maxBatchAllocation = max(maxBatchAllocation, allocated)
				heapUpperBound = max(heapUpperBound, previous.HeapAlloc+allocated)
				previous = sample
				runtime.KeepAlive(held)
			}
			runtime.ReadMemStats(&after)
			elapsed := time.Since(started).Seconds()
			t.Logf("prepared=%d seconds=%.3f prepares_per_sec=%.3f alloc_bytes=%d mallocs=%d sampled_peak_heap=%d baseline_heap=%d final_heap=%d max_batch_alloc_bytes=%d tested_batch_heap_upper_bound=%d", len(refs), elapsed, float64(len(refs))/elapsed, after.TotalAlloc-before.TotalAlloc, after.Mallocs-before.Mallocs, peak, before.HeapAlloc, after.HeapAlloc, maxBatchAllocation, heapUpperBound)
		})
	}
}
