package assembly

import (
	"context"
	_ "embed"
	"encoding/json"
	"os"
	"slices"
	"testing"
	"time"

	"linkd/internal/cleaner"
	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/lifecycle"
	"linkd/internal/lifecycle/kachook"
	"linkd/internal/store/memory"
)

//go:embed testdata/rest_real_host_snapshot.json
var restRealHostSnapshot []byte

type restHostReplayCase struct {
	EventID    string          `json:"event_id"`
	ReceivedAt time.Time       `json:"received_at"`
	Mode       string          `json:"mode"`
	Raw        json.RawMessage `json:"raw"`
}

// 两条原始事件分别补充真实主机的三种身份，验证到 KAC wire payload 的完整本地链路。
// 普通回归使用脱敏夹具；显式设置 LINKD_REST_HOST_REPLAY_INPUT 时读取真实 REST
// 适配器导出的标准事件。此测试没有网络、监控策略或线上写入端口，目录映射为固定夹具。
func TestRESTCapturedAlertWithRealHostReachesKAC(t *testing.T) {
	var host struct {
		Instance struct {
			Tenant     string `json:"bk_tenant_id"`
			Model      string `json:"model_id"`
			ID         string `json:"model_inst_id"`
			UID        string `json:"entity_uid"`
			Display    string `json:"display_name"`
			Attributes struct {
				HostID int64 `json:"bk_host_id"`
			} `json:"attributes"`
		} `json:"instance"`
	}
	if err := json.Unmarshal(restRealHostSnapshot, &host); err != nil {
		t.Fatal(err)
	}
	instance := host.Instance
	if instance.Tenant != "system" || instance.Model != "cw-Host" || instance.ID != "10" || instance.UID != "cw-Host|10" || instance.Attributes.HostID != 10 {
		t.Fatal("captured real host identity changed")
	}
	for _, sample := range loadRESTHostReplayCases(t) {
		t.Run(sample.EventID+"/"+sample.Mode, func(t *testing.T) {
			reader := &restCapturedHostReader{instance: enrich.Instance{TenantID: instance.Tenant, ModelCode: instance.Model, InstanceID: instance.ID,
				Fields: map[string]any{"entity_uid": instance.UID, "display_name": instance.Display}, Attributes: map[string]any{"bk_host_id": instance.Attributes.HostID}}}
			source := config.EventSource{EventSourceID: "built_in_rest", Version: 2,
				Enrich: config.EnrichConfig{Processors: []config.EnrichProcessorConfig{{Type: "resource"}, {Type: "cmdb-access"}}}}
			factory, err := cleaner.NewNormalizationFactory(source, config.DefaultSeverityConfig())
			if err != nil {
				t.Fatal(err)
			}
			raw := cleaner.RawEventMessage{RecordID: sample.EventID, BKTenantID: instance.Tenant, EventSourceID: source.EventSourceID, ReceivedAt: sample.ReceivedAt, Payload: sample.Raw}
			draft, err := (cleaner.StandardCleaner{}).Clean(t.Context(), raw)
			if err != nil {
				t.Fatal(err)
			}
			event, err := factory.Build(raw, draft)
			if err != nil {
				t.Fatal(err)
			}
			router, err := NewRouter([]config.EventSource{source}, enrich.Sources{Model: reader, OneModel: reader, DynamicGroup: reader})
			if err != nil {
				t.Fatal(err)
			}
			repository := memory.New()
			stored, err := repository.CreateEvent(t.Context(), event)
			if err != nil {
				t.Fatal(err)
			}
			processor, err := lifecycle.NewProcessor(repository, lifecycle.NoopRecentAlertCache{}, lifecycle.DeterministicAlertIDGenerator{}, router,
				nil, config.DefaultSeverityConfig(), fixedClock{now: sample.ReceivedAt.Add(time.Second)}, discardLogger{})
			if err != nil {
				t.Fatal(err)
			}
			processed, err := processor.ProcessEvent(t.Context(), stored.StoredEvent)
			if err != nil || len(processed.AlertIDs) != 1 {
				t.Fatalf("alert count=%d error=%v", len(processed.AlertIDs), err)
			}
			storedAlert, err := repository.GetAlert(t.Context(), instance.Tenant, processed.AlertIDs[0])
			if err != nil {
				t.Fatal(err)
			}
			alert := storedAlert.Alert
			payload, err := enrich.DecodePayload(alert.Enrich)
			if err != nil || alert.EnrichStatus != domain.EnrichStatusSucceeded {
				t.Fatalf("enrich status=%s error=%v", alert.EnrichStatus, err)
			}
			if payload.Processors[0]["resource"].Status != domain.EnrichStatusSucceeded || payload.Processors[1]["cmdb-access"].Status != domain.EnrichStatusSucceeded {
				t.Fatal("resource or cmdb-access failed")
			}
			if len(reader.queries) != 1 {
				t.Fatalf("instance queries=%d", len(reader.queries))
			}
			query := reader.queries[0]
			if sample.Mode == "onemodel" && query.InstanceID != "10" || sample.Mode != "onemodel" && len(query.AttributeFilters) != 1 {
				t.Fatal("resource lookup did not use the declared namespace")
			}
			wire, err := kachook.CompatibilityPayload(alert, func(string) (string, error) { return "warning", nil })
			if err != nil {
				t.Fatal(err)
			}
			var message struct {
				ModelID     string   `json:"model_id"`
				ModelInstID string   `json:"model_inst_id"`
				ModelName   string   `json:"model_name"`
				CWLabels    []string `json:"cw_labels"`
			}
			if err := json.Unmarshal(wire, &message); err != nil {
				t.Fatal(err)
			}
			if message.ModelID != "cw-Host" || message.ModelInstID != "10" || message.ModelName != "主机" || !slices.Contains(message.CWLabels, "cw-Host|10") {
				t.Fatalf("unexpected KAC model=%q instance=%q name=%q permission=%v", message.ModelID, message.ModelInstID, message.ModelName, message.CWLabels)
			}
			t.Logf("KAC wire: model_id=%s model_inst_id=%s model_name=%s permission=cw-Host|10", message.ModelID, message.ModelInstID, message.ModelName)
		})
	}
}

func loadRESTHostReplayCases(t *testing.T) []restHostReplayCase {
	t.Helper()
	if path := os.Getenv("LINKD_REST_HOST_REPLAY_INPUT"); path != "" {
		info, err := os.Stat(path) //nolint:gosec // 操作者显式选择的本地集成测试输入，限制大小后读取。
		if err != nil || info.Size() > 1<<20 {
			t.Fatal("captured adapter output unavailable or oversized")
		}
		data, err := os.ReadFile(path) //nolint:gosec // 同上，不接受服务请求路径。
		var input struct {
			Cases []restHostReplayCase `json:"cases"`
		}
		if err != nil || json.Unmarshal(data, &input) != nil || len(input.Cases) != 6 {
			t.Fatal("expected six adapter-normalized captured inputs")
		}
		return input.Cases
	}
	var snapshot struct {
		Events []restHostReplayCase `json:"events"`
	}
	if err := json.Unmarshal(restMissingIdentitySnapshot, &snapshot); err != nil || len(snapshot.Events) != 2 {
		t.Fatal("invalid captured event fixture")
	}
	var cases []restHostReplayCase
	for _, sample := range snapshot.Events {
		for _, mode := range []string{"cmdb", "onemodel", "legacy"} {
			var raw map[string]json.RawMessage
			if err := json.Unmarshal(sample.Raw, &raw); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "cmdb":
				raw["subject"] = json.RawMessage(`{"system":"cmdb","type":"host","id":"10"}`)
			case "onemodel":
				raw["subject"] = json.RawMessage(`{"system":"onemodel","type":"cw-Host","id":"10"}`)
			case "legacy":
				// 顶层旧字段先由真实 REST 适配器映射到标准 extra_data；普通 Go 回归固定该输出。
				raw["extra_data"] = json.RawMessage(`{"bk_obj_id":"host","bk_inst_id":"10"}`)
			}
			data, err := json.Marshal(raw)
			if err != nil {
				t.Fatal(err)
			}
			cases = append(cases, restHostReplayCase{EventID: sample.EventID, ReceivedAt: sample.ReceivedAt, Mode: mode, Raw: data})
		}
	}
	return cases
}

type restCapturedHostReader struct {
	instance enrich.Instance
	queries  []enrich.InstanceQuery
}

func (r *restCapturedHostReader) GetModelByCode(_ context.Context, tenant, code string) (enrich.Model, bool, error) {
	if tenant != r.instance.TenantID || code != r.instance.ModelCode {
		return enrich.Model{}, false, nil
	}
	return enrich.Model{TenantID: tenant, ModelID: "20", ModelCode: code, Fields: map[string]any{"object_model_name": "主机", "bk_cmdb_obj_id": "host"}}, true, nil
}

func (r *restCapturedHostReader) GetModelByCMDBObjectID(ctx context.Context, tenant, object string) (enrich.Model, bool, error) {
	if object != "host" {
		return enrich.Model{}, false, nil
	}
	return r.GetModelByCode(ctx, tenant, r.instance.ModelCode)
}

func (r *restCapturedHostReader) FindInstance(_ context.Context, tenant string, query enrich.InstanceQuery) (enrich.Instance, bool, error) {
	r.queries = append(r.queries, query)
	if tenant != r.instance.TenantID || query.ModelCode != r.instance.ModelCode {
		return enrich.Instance{}, false, enrich.ErrInvalidDataSourceResponse
	}
	if query.InstanceID != "" {
		return r.instance, query.InstanceID == r.instance.InstanceID, nil
	}
	if len(query.AttributeFilters) != 1 {
		return enrich.Instance{}, false, nil
	}
	filter := query.AttributeFilters[0]
	return r.instance, filter.Field == "bk_host_id" && filter.Type == enrich.InstanceAttributeLong && filter.Value == int64(10), nil
}

func (r *restCapturedHostReader) GetDynamicGroupIDs(_ context.Context, tenant, model, instance string) ([]string, error) {
	if tenant != r.instance.TenantID || model != r.instance.ModelCode || instance != r.instance.InstanceID {
		return nil, enrich.ErrInvalidDataSourceResponse
	}
	return []string{}, nil
}
