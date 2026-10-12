// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package telemetry

import (
	"errors"
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"linkd/internal/lifecycle"
)

func TestLifecyclePhaseMetricsKeepZeroSamplesAndAttemptOutcomes(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = provider.Shutdown(t.Context()) })
	metrics, err := newInstruments(provider.Meter("test"))
	if err != nil {
		t.Fatal(err)
	}
	observer := (&Runtime{metrics: metrics}).LifecyclePhaseObserver()
	for _, attemptErr := range []error{nil, errors.New("failed attempt")} {
		observer.Finished(t.Context(), lifecycle.PhaseObservation{
			Phases: []lifecycle.PhaseDuration{{Phase: "enrich", Duration: 2 * time.Millisecond, BatchWait: time.Millisecond, StorageQueue: time.Millisecond / 2}, {Phase: "final_hook", Duration: 0}}, Err: attemptErr,
			Steps: []lifecycle.StepDuration{{PhaseDuration: lifecycle.PhaseDuration{Phase: "enrich", Duration: 2 * time.Millisecond, BatchWait: time.Millisecond, StorageQueue: time.Millisecond / 2}, Step: "persist"}, {PhaseDuration: lifecycle.PhaseDuration{Phase: "enrich"}, Step: "custom"}},
		})
	}
	var data metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &data); err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, scope := range data.ScopeMetrics {
		for _, metric := range scope.Metrics {
			if metric.Name == "linkd.lifecycle.event.phase.step.duration" {
				histogram := metric.Data.(metricdata.Histogram[float64])
				if len(histogram.DataPoints) != 12 {
					t.Fatal(histogram)
				}
				for _, point := range histogram.DataPoints {
					if point.Count != 1 || point.Attributes.Len() != 4 {
						t.Fatal(point)
					}
					step, _ := point.Attributes.Value("linkd.step")
					kind, _ := point.Attributes.Value("linkd.wait_kind")
					want := 0.0005
					if kind.AsString() == "batch_wait" {
						want = 0.001
					}
					if step.AsString() == "custom" {
						want = 0
					}
					if point.Sum != want {
						t.Fatal(point)
					}
				}
				seen++
				continue
			}
			if metric.Name != "linkd.lifecycle.event.phase.duration" && metric.Name != "linkd.lifecycle.event.phase.execution_duration" && metric.Name != "linkd.lifecycle.event.phase.wait_duration" {
				continue
			}
			seen++
			wantPoints, wantAttrs := 4, 2
			if metric.Name == "linkd.lifecycle.event.phase.wait_duration" {
				wantPoints, wantAttrs = 8, 3
			}
			histogram, ok := metric.Data.(metricdata.Histogram[float64])
			if !ok || len(histogram.DataPoints) != wantPoints {
				t.Fatalf("unexpected phase data: %#v", metric.Data)
			}
			for _, point := range histogram.DataPoints {
				if point.Count != 1 || point.Attributes.Len() != wantAttrs {
					t.Fatalf("unexpected sample: %#v", point)
				}
				phase, _ := point.Attributes.Value("linkd.phase")
				outcome, _ := point.Attributes.Value("linkd.outcome")
				if outcome.AsString() != "accepted" && outcome.AsString() != "failed" {
					t.Fatal(outcome)
				}
				want := 0.002
				if metric.Name == "linkd.lifecycle.event.phase.execution_duration" {
					want = 0.0005
				}
				if metric.Name == "linkd.lifecycle.event.phase.wait_duration" {
					kind, _ := point.Attributes.Value("linkd.wait_kind")
					if kind.AsString() == "batch_wait" {
						want = 0.001
					} else if kind.AsString() == "storage_queue" {
						want = 0.0005
					} else {
						t.Fatal(kind)
					}
				}
				if phase.AsString() == "final_hook" {
					want = 0
				}
				if point.Sum != want {
					t.Fatalf("phase %s sum=%v", phase.AsString(), point.Sum)
				}
			}
		}
	}
	if seen != 4 {
		t.Fatalf("phase metric families=%d", seen)
	}
}
