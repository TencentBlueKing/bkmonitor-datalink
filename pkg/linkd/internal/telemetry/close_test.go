// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package telemetry

import (
	"context"
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"linkd/internal/store"
)

func TestClosePhaseObserverRecordsFixedLabelsAfterCancellation(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = provider.Shutdown(t.Context()) })
	metrics, err := newInstruments(provider.Meter("test"))
	if err != nil {
		t.Fatal(err)
	}
	r := &Runtime{metrics: metrics}
	base, cancel := context.WithCancel(t.Context())
	cancel()
	ctx := store.WithBatchPhaseObserver(base, r.ObserveClosePhase)
	store.ObserveBatchPhase(ctx, "batch.cas", 12*time.Millisecond)
	store.ObserveBatchPhase(ctx, "member.lease_release", 2*time.Millisecond)
	var data metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &data); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, scope := range data.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name != "linkd.close.phase_duration" {
				continue
			}
			found = true
			h := m.Data.(metricdata.Histogram[float64])
			if len(h.DataPoints) != 2 {
				t.Fatal(h.DataPoints)
			}
			for _, p := range h.DataPoints {
				if p.Attributes.Len() != 1 || p.Count != 1 || p.Sum <= 0 {
					t.Fatal(p)
				}
			}
		}
	}
	if !found {
		t.Fatal("missing close histogram")
	}
}
