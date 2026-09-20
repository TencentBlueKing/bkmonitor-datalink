// Tencent is pleased to support the open source community. All rights reserved.
// Licensed under the MIT License.

package enrich

import (
	"context"
	"reflect"
	"testing"

	"linkd/internal/lifecycle/enrich/models"
)

func TestScopeCachesReadersByStableQueryKey(t *testing.T) {
	t.Parallel()
	reader := &scopeCacheReader{}
	scope, err := NewScope(testAlert(), Sources{
		Metric: reader, Model: reader, OneModel: reader, CollectConfig: reader,
		Uptime: reader, UptimeNode: reader, CollectTopology: reader,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	queryA := InstanceQuery{ModelCode: "cw-Service", InstanceID: "1", AttributeFilters: []InstanceAttributeFilter{
		{Field: "cw_biz_id", Type: InstanceAttributeLong, Value: int64(2)},
		{Field: "name", Type: InstanceAttributeKeyword, Value: "svc"},
	}}
	queryAReordered := InstanceQuery{ModelCode: "cw-Service", InstanceID: "1", AttributeFilters: []InstanceAttributeFilter{
		{Field: "name", Type: InstanceAttributeKeyword, Value: "svc"},
		{Field: "cw_biz_id", Type: InstanceAttributeLong, Value: int64(2)},
	}}
	queryB := InstanceQuery{ModelCode: "cw-Service", InstanceID: "2"}
	for _, query := range []InstanceQuery{queryA, queryAReordered, queryB, queryB} {
		if _, _, err := scope.Instance(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		_, _, _ = scope.ModelByCode(ctx, "cw-Service")
		_, _, _ = scope.MetricLibrary(ctx, models.MetricLibraryQuery{TableID: "service.metric", FieldName: "available"})
		_, _, _ = scope.CollectConfig(ctx, "collect-1")
		_, _, _ = scope.UptimeTask(ctx, "7")
		_, _, _ = scope.UptimeNode(ctx, "0:10.0.0.8")
		_, _, _ = scope.RelatedHost(ctx, "cw-Service", "1", "service_run_host")
		_, _, _ = scope.HostTopology(ctx, "101")
	}
	if reader.instanceCalls != 2 || reader.modelCalls != 1 || reader.metricCalls != 1 ||
		reader.collectCalls != 1 || reader.uptimeCalls != 1 || reader.nodeCalls != 1 ||
		reader.relatedCalls != 1 || reader.topologyCalls != 1 {
		t.Fatalf("calls=%#v", reader)
	}
	if !reflect.DeepEqual(reader.instanceIDs, []string{"1", "2"}) {
		t.Fatalf("instance IDs=%v", reader.instanceIDs)
	}
}

func TestScopeDoesNotCacheCanceledLookup(t *testing.T) {
	reader := &scopeCacheReader{}
	scope, err := NewScope(testAlert(), Sources{OneModel: reader})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	query := InstanceQuery{ModelCode: "cw-Host", InstanceID: "1"}
	_, _, _ = scope.Instance(ctx, query)
	_, _, _ = scope.Instance(context.Background(), query)
	if reader.instanceCalls != 2 {
		t.Fatalf("instance calls=%d", reader.instanceCalls)
	}
}

type scopeCacheReader struct {
	instanceCalls int
	instanceIDs   []string
	modelCalls    int
	metricCalls   int
	collectCalls  int
	uptimeCalls   int
	nodeCalls     int
	relatedCalls  int
	topologyCalls int
}

func (r *scopeCacheReader) FindInstance(_ context.Context, tenantID string, query InstanceQuery) (Instance, bool, error) {
	r.instanceCalls++
	r.instanceIDs = append(r.instanceIDs, query.InstanceID)
	return Instance{TenantID: tenantID, ModelCode: query.ModelCode, InstanceID: query.InstanceID}, true, nil
}

func (r *scopeCacheReader) GetModelByCode(_ context.Context, tenantID, modelCode string) (Model, bool, error) {
	r.modelCalls++
	return Model{TenantID: tenantID, ModelID: modelCode, ModelCode: modelCode}, true, nil
}

func (r *scopeCacheReader) FindMetricLibrary(context.Context, models.MetricLibraryQuery) (models.MetricMetadata, bool, error) {
	r.metricCalls++
	return models.MetricMetadata{}, true, nil
}

func (r *scopeCacheReader) GetCollectConfig(context.Context, string, string) (models.CollectConfig, bool, error) {
	r.collectCalls++
	return models.CollectConfig{}, true, nil
}

func (r *scopeCacheReader) GetUptimeTask(context.Context, string, string) (models.UptimeTask, bool, error) {
	r.uptimeCalls++
	return models.UptimeTask{}, true, nil
}

func (r *scopeCacheReader) GetUptimeNode(context.Context, string, string) (models.UptimeNode, bool, error) {
	r.nodeCalls++
	return models.UptimeNode{}, true, nil
}

func (r *scopeCacheReader) FindRelatedHost(context.Context, string, string, string, string) (Instance, bool, error) {
	r.relatedCalls++
	return Instance{}, true, nil
}

func (r *scopeCacheReader) FindHostTopology(context.Context, string, string) (models.ResourceTopology, bool, error) {
	r.topologyCalls++
	return models.ResourceTopology{}, true, nil
}
