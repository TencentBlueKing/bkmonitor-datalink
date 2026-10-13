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
	"context"
	"errors"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"linkd/internal/actiondelivery"
	"linkd/internal/domain"
	"linkd/internal/lifecycle"
	"linkd/internal/projection"
)

// 正常背压可以重复观察，但不能伪造成消息失败；混合异常仍保留失败分类。
func TestLifecycleMetricsSeparateBackpressureFromFailures(t *testing.T) {
	t.Parallel()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	instruments, err := newInstruments(provider.Meter("test"))
	if err != nil {
		t.Fatal(err)
	}
	observer := (&Runtime{metrics: instruments}).LifecycleSchedulerObserver()
	cases := []struct {
		err  error
		want string
	}{
		{actiondelivery.ErrBusy, "deferred"},
		{actiondelivery.ErrCapacity, "deferred"},
		{errors.Join(actiondelivery.ErrBusy, projection.ErrBusy), "deferred"},
		{errors.Join(actiondelivery.ErrBusy, errors.New("backend failed")), "failed"},
		{errors.Join(actiondelivery.ErrBusy, context.Canceled), "failed"},
		{actiondelivery.ErrInvalid, "failed"},
	}
	for _, tc := range cases {
		if got := lifecycleMetricOutcome(lifecycle.ProcessResult{}, tc.err); got != tc.want {
			t.Fatalf("got %s want %s", got, tc.want)
		}
		observer.EventProcessed(t.Context(), "test", domain.EventActionTriggered, lifecycle.ProcessResult{}, tc.err)
	}
	var data metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &data); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, scope := range data.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name != "linkd.lifecycle.result.items" {
				continue
			}
			found = true
			sum := m.Data.(metricdata.Sum[int64])
			counts := map[string]int64{}
			for _, point := range sum.DataPoints {
				outcome, _ := point.Attributes.Value("linkd.outcome")
				counts[outcome.AsString()] += point.Value
			}
			if counts["deferred"] != 3 || counts["failed"] != 3 || len(counts) != 2 {
				t.Fatal(counts)
			}
		}
	}
	if !found {
		t.Fatal("lifecycle result metric missing")
	}
}
