// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/worker"
)

// errMeasured stops the Slot once its series are kept and measured.
var errMeasured = errors.New("measured")

// TestMeasureTheStreamedInputChargeAgainstTheHeap holds what a Slot charges
// its input phase for the series it keeps to the heap they take: N series of
// P points, built as the UQ decoder builds them (the largest Query Group's
// three dimensions, record ids derived, not kept) and bound as the access
// source binds them, handed to the Slot one batch a series, the charge read
// once every series is kept and the heap measured as the HeapAlloc the batches
// and what the Slot keeps of them add once the garbage is collected. Their
// ratio must fall within [0.8, 1.25] at 1, 5, 15 and 30 points; the charge
// at each, a series and its Dataset's part, gives what a point costs. A measurement of
// hundreds of MiB, run on demand with ALARMD_MEASURE_INPUT_HEAP=1.
func TestMeasureTheStreamedInputChargeAgainstTheHeap(t *testing.T) {
	if os.Getenv("ALARMD_MEASURE_INPUT_HEAP") == "" {
		t.Skip("a measurement: set ALARMD_MEASURE_INPUT_HEAP=1")
	}
	const series = 100_000
	for _, points := range []int{1, 5, 15, 30} {
		header, batches := workerG4StreamFixture(t, strategy.DetectorKindSimpleRingRatio)
		primary, dependency := shareFixtureQueries(t, header)
		template := batches[primary.index].Inputs[0]
		window := template.QueryWindow
		step := (window.End - window.Start) / int64(points)
		dependencyTemplate := batches[dependency.index].Inputs[0]
		dependencyWindow := dependencyTemplate.QueryWindow
		dependencyStep := (dependencyWindow.End - dependencyWindow.Start) / int64(points)
		var charged, heap, datasets uint64
		// A budget the series fit whole: the measurement is of the charge,
		// not of a refusal.
		ports, _, coordinator := workerG4CoordinatorWithBudget(t, observability.ObserverFunc(func(context.Context, observability.Observation) {}),
			nil, worker.ProvisionalBudget{MaxSeries: 2 * series, MaxRetainedBytes: 8 << 30,
				MaxStateMutations: 100, MaxEvents: 100, MaxGapMutations: 10})
		ports.gapMissing = true
		ports.executeOverride = func(ctx context.Context, _ execution.QueryExecutionRequest, consumer execution.QueryExecutionConsumer) (execution.QueryExecutionCompletion, error) {
			if err := consumer.Begin(ctx, header); err != nil {
				return execution.QueryExecutionCompletion{}, err
			}
			var before, after runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&before)
			start := worker.RetainedInputOf(consumer)
			// Each series from both queries, as the decoder hands them on: one
			// run a series, every point of an answer received at the one
			// moment the answer was (access/uq/client.go), the dependency's
			// without its dimensions (access/source.go).
			for index := 0; index < series; index++ {
				batch, err := measuredSeriesBatch(batches[primary.index], template, index, points, window.Start, step, window.End, false)
				if err != nil {
					return execution.QueryExecutionCompletion{}, err
				}
				previous, err := measuredSeriesBatch(batches[dependency.index], dependencyTemplate, index, points,
					dependencyWindow.Start, dependencyStep, dependencyWindow.End, true)
				if err != nil {
					return execution.QueryExecutionCompletion{}, err
				}
				datasets += batch.Dataset.RetainedBytes() + previous.Dataset.RetainedBytes()
				if err := consumer.ConsumeSeries(ctx, batch); err != nil {
					return execution.QueryExecutionCompletion{}, err
				}
				if err := consumer.ConsumeSeries(ctx, previous); err != nil {
					return execution.QueryExecutionCompletion{}, err
				}
			}
			runtime.GC()
			runtime.ReadMemStats(&after)
			charged, heap = worker.RetainedInputOf(consumer)-start, after.HeapAlloc-before.HeapAlloc
			runtime.KeepAlive(consumer)
			return execution.QueryExecutionCompletion{}, errMeasured
		}
		if _, err := coordinator.Execute(context.Background(), workerSlotRequest(header.Contract)); !errors.Is(err, errMeasured) {
			t.Fatalf("points=%d: the Slot ended %v before its series were measured", points, err)
		}
		ratio := float64(charged) / float64(heap)
		t.Logf("series=%d points=%d charged=%d (%d a series, its two Datasets %d) heap=%d (%d a series) ratio=%.3f",
			series, points, charged, charged/series, datasets/series, heap, heap/series, ratio)
		if ratio < 0.8 || ratio > 1.25 {
			t.Errorf("points=%d: charged %d for a heap of %d (ratio %.3f), want within [0.8, 1.25]", points, charged, heap, ratio)
		}
	}
}

// measuredSeriesBatch is series index of the measurement: its records over
// the query's window, one a step, and its binding as the access source makes
// it.
func measuredSeriesBatch(
	shape execution.SeriesExecutionBatch, template execution.NamedInputBinding,
	index, points int, start, step, receivedAt int64, dependency bool,
) (execution.SeriesExecutionBatch, error) {
	dimensions := map[string]json.RawMessage{
		"bk_target_cloud_id": json.RawMessage(fmt.Sprintf(`"%03d"`, index%1000)),
		"bk_target_ip":       json.RawMessage(fmt.Sprintf(`"198.51.100.%03d"`, index%256)),
		"device_name":        json.RawMessage(fmt.Sprintf(`"device-%025d"`, index)),
	}
	fields := []contract.DimensionFieldV2{{Name: "bk_target_cloud_id", Value: dimensions["bk_target_cloud_id"]},
		{Name: "bk_target_ip", Value: dimensions["bk_target_ip"]}, {Name: "device_name", Value: dimensions["device_name"]}}
	identity, err := contract.EncodeDimensionIdentityV2("tenant", "2", fields)
	if err != nil {
		return execution.SeriesExecutionBatch{}, err
	}
	records := make([]contract.CanonicalRecordV2, 0, points)
	for point := 0; point < points; point++ {
		at := start + int64(point)*step
		recordID, err := contract.DeriveRecordIDV2(identity.Digest, at)
		if err != nil {
			return execution.SeriesExecutionBatch{}, err
		}
		records = append(records, contract.CanonicalRecordV2{RecordID: recordID, SourceTime: at, BusinessID: "2",
			DimensionIdentity: contract.DimensionIdentityV2{Fields: fields, Digest: identity.Digest},
			Values:            map[string]json.RawMessage{"value": json.RawMessage(`12.5`)},
			Dimensions:        dimensions, ReceivedTime: receivedAt})
	}
	dataset := execution.NewDatasetOfDerivedRecordIDs(records)
	if dependency {
		dataset = dataset.WithoutDimensions()
	}
	digest, err := contract.DeriveSeriesRecordsDigestV2("alarmd-provider-series-delivery-v1", records, identity)
	if err != nil {
		return execution.SeriesExecutionBatch{}, err
	}
	ordinals := make([]uint32, len(records))
	for ordinal := range ordinals {
		ordinals[ordinal] = uint32(ordinal)
	}
	view, err := execution.NewDatasetView(dataset, ordinals)
	if err != nil {
		return execution.SeriesExecutionBatch{}, err
	}
	binding := template
	binding.Dataset, binding.View = dataset, view
	batch := shape
	batch.Dataset, batch.Inputs = dataset, []execution.NamedInputBinding{binding}
	batch.Delivery.Series, batch.Delivery.Records, batch.Delivery.Digest = 1, uint64(len(records)), digest
	return batch, nil
}
