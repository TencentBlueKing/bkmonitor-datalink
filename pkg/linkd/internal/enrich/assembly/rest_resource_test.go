package assembly

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/datasources"
	"linkd/internal/lifecycle"
	"linkd/internal/lifecycle/kachook"
	"linkd/internal/store/memory"
)

// 验证真实 Router、生命周期、权限收尾与 KAC 投影；没有配置任何监控策略 Reader。
func TestRESTSubjectReachesKACModelAndInstance(t *testing.T) {
	t.Parallel()
	for _, system := range []string{"cmdb", "onemodel"} {
		t.Run(system, func(t *testing.T) {
			reader := restResourceReader{}
			router, err := NewRouter([]config.EventSource{{EventSourceID: "built_in_rest", Version: 1,
				Enrich: config.EnrichConfig{Processors: []config.EnrichProcessorConfig{{Type: "resource"}, {Type: "cmdb-access"}}},
			}}, enrich.Sources{Model: reader, OneModel: reader, DynamicGroup: reader})
			if err != nil {
				t.Fatal(err)
			}
			event := baseCollectAlert("built_in_rest")
			event.BKTenantID = "tenant-a"
			event.Labels, event.Dimensions = domain.DimensionMap{}, domain.DimensionMap{}
			event.SubjectSystem, event.SubjectType, event.SubjectID = system, "cw-Host", "101"
			if system == "cmdb" {
				event.SubjectType = "host"
			}
			repository := memory.New()
			stored, err := repository.CreateEvent(t.Context(), event)
			if err != nil {
				t.Fatal(err)
			}
			processor, err := lifecycle.NewProcessor(repository, lifecycle.NoopRecentAlertCache{}, lifecycle.DeterministicAlertIDGenerator{}, router,
				nil, config.DefaultSeverityConfig(), fixedClock{now: event.CreateAt.Add(time.Second)}, discardLogger{})
			if err != nil {
				t.Fatal(err)
			}
			processed, err := processor.ProcessEvent(t.Context(), stored.StoredEvent)
			if err != nil || len(processed.AlertIDs) != 1 {
				t.Fatalf("processed=%+v error=%v", processed, err)
			}
			storedAlert, err := repository.GetAlert(t.Context(), event.BKTenantID, processed.AlertIDs[0])
			if err != nil {
				t.Fatal(err)
			}
			alert := storedAlert.Alert
			message, err := kachook.CompatibilityMessage(alert, func(string) (string, error) { return "warning", nil })
			if err != nil || alert.EnrichStatus != domain.EnrichStatusSucceeded || message.ModelID != "cw-Host" || message.ModelInstID != "101" || message.ModelName != "主机" ||
				!slices.Contains(message.CWLabels, "cw-Host|101") || !slices.Contains(message.DynamicGroupID, "group-a") {
				t.Fatalf("KAC message=%+v status=%s error=%v", message, alert.EnrichStatus, err)
			}
		})
	}
}

type restResourceReader struct{}

func (restResourceReader) GetModelByCode(_ context.Context, tenant, code string) (enrich.Model, bool, error) {
	return enrich.Model{TenantID: tenant, ModelID: "20", ModelCode: code, Fields: map[string]any{"object_model_name": "主机", "bk_cmdb_obj_id": "host"}}, true, nil
}

func (r restResourceReader) GetModelByCMDBObjectID(ctx context.Context, tenant, _ string) (enrich.Model, bool, error) {
	return r.GetModelByCode(ctx, tenant, "cw-Host")
}

func (restResourceReader) FindInstance(_ context.Context, tenant string, query enrich.InstanceQuery) (enrich.Instance, bool, error) {
	return enrich.Instance{TenantID: tenant, ModelCode: query.ModelCode, InstanceID: "101", Fields: map[string]any{"entity_uid": query.ModelCode + "|101"}, Attributes: map[string]any{"bk_host_id": int64(101)}}, true, nil
}

func (restResourceReader) GetDynamicGroupIDs(_ context.Context, tenant, model, instance string) ([]string, error) {
	if tenant != "tenant-a" || model != "cw-Host" || instance != "101" {
		return nil, enrich.ErrInvalidDataSourceResponse
	}
	return []string{"group-a"}, nil
}

func TestFrozenMonitorReleaseKeepsResourceMeaning(t *testing.T) {
	t.Parallel()
	source := config.EventSource{EventSourceID: "built_in_bk", Version: 3, Enrich: config.EnrichConfig{Processors: []config.EnrichProcessorConfig{{Type: "strategy"}, {Type: "resource"}}}}
	if !legacyMonitorResource(source) {
		t.Fatal("frozen monitoring chain lost its execution meaning")
	}
	reader := (&datasources.Mock{}).Sources()
	legacyRouter, err := NewRouter([]config.EventSource{source}, reader)
	if err != nil {
		t.Fatal(err)
	}
	event := baseCollectAlert("built_in_bk")
	legacyResult, err := legacyRouter.Enrich(t.Context(), enrich.Input{Event: event})
	if err != nil {
		t.Fatal(err)
	}
	currentSource := source
	currentSource.Enrich.Processors = []config.EnrichProcessorConfig{{Type: "strategy"}, {Type: "monitor-resource"}}
	currentRouter, err := NewRouter([]config.EventSource{currentSource}, reader)
	if err != nil {
		t.Fatal(err)
	}
	currentResult, err := currentRouter.Enrich(t.Context(), enrich.Input{Event: event})
	if err != nil {
		t.Fatal(err)
	}
	legacyPayload := string(legacyResult.Data.Evaluations[0].Data["processors"])
	currentPayload := strings.ReplaceAll(string(currentResult.Data.Evaluations[0].Data["processors"]), `"monitor-resource":`, `"resource":`)
	if legacyPayload != currentPayload {
		t.Fatalf("frozen monitor resource changed: old=%s new=%s", legacyPayload, currentPayload)
	}
	source.EventSourceID = "built_in_rest"
	if legacyMonitorResource(source) {
		t.Fatal("REST dispatched to monitoring resource")
	}
	source.EventSourceID = "built_in_bk"
	source.Version = 0
	if legacyMonitorResource(source) {
		t.Fatal("unpublished chain treated as historical release")
	}
	source.Version = 4
	source.Enrich.Processors[1].Type = "monitor-resource"
	if legacyMonitorResource(source) {
		t.Fatal("new monitoring configuration treated as legacy")
	}
}
