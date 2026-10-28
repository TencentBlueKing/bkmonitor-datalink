// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package processors

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"linkd/internal/enrich"
	"linkd/internal/enrich/models"
	"linkd/internal/enrich/rules"
)

type promQLDimensionReader struct {
	*promQLSnapshotReader
	dimensions []models.MetricDimension
}

func (r *promQLDimensionReader) FindMetric(ctx context.Context, query models.MetricQuery) (models.MetricMetadata, bool, error) {
	metadata, found, err := r.promQLSnapshotReader.FindMetric(ctx, query)
	metadata.Dimensions = r.dimensions
	return metadata, found, err
}

func TestPromQLDisplayUsesReturnedDimensions(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		dimensions map[string]any
		aggregate  []string
		additional map[string]any
		noCatalog  bool
		ordinary   bool
		wantKeys   []string
		wantNames  []string
	}{
		{
			name:       "labels without declared grouping",
			dimensions: map[string]any{"device_name": "cpu-total", "hostname": "host-1", "zone": "a"},
			wantKeys:   []string{"device_name", "hostname", "zone"}, wantNames: []string{"设备名", "hostname", "zone"},
		},
		{
			name: "missing catalog retains unknown names and zero values", noCatalog: true,
			dimensions: map[string]any{"device_name": "cpu-total", "attempt": float64(0), "enabled": false},
			wantKeys:   []string{"attempt", "device_name", "enabled"}, wantNames: []string{"attempt", "device_name", "enabled"},
		},
		{
			name: "aggregation only displays returned labels", aggregate: []string{"device_name", "missing"},
			dimensions: map[string]any{"hostname": "host-1"},
			wantKeys:   []string{"hostname"}, wantNames: []string{"hostname"},
		},
		{
			name:       "model identities and additional labels occur once",
			dimensions: map[string]any{"model_id": "cw-Host", "model_inst_id": "101", "hostname": "host-1"},
			additional: map[string]any{"service": "api"},
			wantKeys:   []string{"model_id", "model_inst_id", "hostname", "service"},
			wantNames:  []string{"对象模型ID", "对象模型实例ID", "hostname", "service"},
		},
		{
			name: "empty result labels stay empty", dimensions: map[string]any{},
			wantKeys: []string{}, wantNames: []string{},
		},
		{
			name:       "no data control marker is not a result label",
			dimensions: map[string]any{rules.FieldNoDataDimension: "true", "hostname": "host-1"},
			wantKeys:   []string{"hostname"}, wantNames: []string{"hostname"},
		},
		{
			name:       "additional no data marker is not reintroduced",
			dimensions: map[string]any{"hostname": "host-1"},
			additional: map[string]any{rules.FieldNoDataDimension: "true"},
			wantKeys:   []string{"hostname"}, wantNames: []string{"hostname"},
		},
		{
			name: "ordinary metrics retain declared grouping", ordinary: true, aggregate: []string{"device_name"},
			dimensions: map[string]any{"device_name": "cpu-total", "hostname": "host-1"},
			wantKeys:   []string{"device_name"}, wantNames: []string{"设备名"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fixture := loadPromQLSnapshotFixture(t)
			event := fixture.alert(t)
			event.Evaluations = event.Evaluations[:1]
			event.Dimensions = fixtureDimensions(t, tc.dimensions)
			original := event.Clone()
			additional := fixtureDimensions(t, tc.additional)
			combined := event.Dimensions.Clone()
			for key, value := range additional {
				combined[key] = value
			}
			strategy := fixture.strategy()
			strategy.Spec.StrategyItem.QueryConfigs[0].AggregateBy = tc.aggregate
			if tc.ordinary {
				query := &strategy.Spec.StrategyItem.QueryConfigs[0]
				query.QueryConfigType, query.PromQL = "", ""
				query.ResultTableID, query.MetricField = "system.cpu_summary", "usage"
			}
			projection, err := strategy.StrategyItemProjection()
			if err != nil {
				t.Fatal(err)
			}
			reader := &promQLDimensionReader{
				promQLSnapshotReader: &promQLSnapshotReader{fixture: fixture, strategy: strategy, withoutMetric: tc.noCatalog},
				dimensions:           []models.MetricDimension{{Key: "device_name", Name: "设备名"}, {Key: "hostname", Name: ""}},
			}
			scope, err := enrich.NewScope(event, enrich.Sources{Metric: reader})
			if err != nil {
				t.Fatal(err)
			}
			got, err := buildDisplayDimensions(t.Context(), scope, strategy, projection, combined, additional)
			if err != nil {
				t.Fatal(err)
			}
			keys, names := []string{}, []string{}
			for _, dimension := range got {
				if dimension.RealKey == nil || dimension.RealValue == nil || *dimension.RealValue != combined[*dimension.RealKey] {
					t.Fatalf("lost original dimension identity: %#v", dimension)
				}
				keys, names = append(keys, *dimension.RealKey), append(names, dimension.Name)
			}
			if !reflect.DeepEqual(keys, tc.wantKeys) || !reflect.DeepEqual(names, tc.wantNames) {
				t.Fatalf("keys=%v names=%v; want keys=%v names=%v", keys, names, tc.wantKeys, tc.wantNames)
			}
			text := buildDimensionText(got, rules.MainData, models.ResourceValues{}, models.StrategyQueryConfig{}, additional)
			if len(tc.wantKeys) > 0 && !strings.Contains(text, tc.wantNames[0]) {
				t.Fatalf("dimension text is missing returned label: %q", text)
			}
			if !reflect.DeepEqual(event, original) {
				t.Fatal("display mutated original event")
			}
		})
	}
}

func TestPromQLDisplayDimensionLookupPropagatesCancellation(t *testing.T) {
	t.Parallel()
	fixture := loadPromQLSnapshotFixture(t)
	event := fixture.alert(t)
	event.Evaluations = event.Evaluations[:1]
	strategy := fixture.strategy()
	projection, err := strategy.StrategyItemProjection()
	if err != nil {
		t.Fatal(err)
	}
	scope, err := enrich.NewScope(event, enrich.Sources{Metric: &promQLSnapshotReader{fixture: fixture}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := buildDisplayDimensions(ctx, scope, strategy, projection, event.Dimensions, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation was hidden: %v", err)
	}
}
