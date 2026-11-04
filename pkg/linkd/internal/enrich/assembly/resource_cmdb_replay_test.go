package assembly

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/lifecycle"
	"linkd/internal/lifecycle/kachook"
	"linkd/internal/store/memory"
)

// TestCMDBCapturedIdentityReplay 在本地重算抓取的原 Event，并重放到 KAC 输出边界。
// 只使用显式提供的实例快照和内存存储，不配置网络、业务写入或通知端口。
func TestCMDBCapturedIdentityReplay(t *testing.T) {
	path := os.Getenv("LINKD_CMDB_IDENTITY_REPLAY_FILE")
	if path == "" {
		t.Skip("requires captured Event and canonical OneModel instance snapshots")
	}
	info, err := os.Stat(path) //nolint:gosec // 操作者显式指定的本地快照，限制读取大小。
	if err != nil || info.Size() > 1<<20 {
		t.Fatal("captured replay file unavailable or oversized")
	}
	raw, err := os.ReadFile(path) //nolint:gosec // 同上，不接收服务端请求路径。
	var samples []cmdbIdentityReplaySample
	if err != nil || json.Unmarshal(raw, &samples) != nil || len(samples) != 2 {
		t.Fatal("expected two captured identity samples")
	}
	for _, sample := range samples {
		t.Run(sample.Event.EventID, func(t *testing.T) {
			reader := cmdbIdentityReplayReader{sample: sample}
			source := config.EventSource{EventSourceID: sample.Event.EventSourceID, Version: sample.Event.EventSourceVersion,
				Enrich: config.EnrichConfig{Processors: []config.EnrichProcessorConfig{{Type: "resource"}}}}
			router, err := NewRouter([]config.EventSource{source}, enrich.Sources{Model: reader, OneModel: reader})
			if err != nil {
				t.Fatal(err)
			}
			// 只重置处理结果；原身份声明、正文、维度和发生时间不变。
			event := sample.Event.Clone()
			event.EventEnrichment = domain.EventEnrichment{}
			event.RelatedAlertIDs = nil
			before := event.Clone()
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
				t.Fatalf("local lifecycle result=%+v error=%v", processed, err)
			}
			alert, err := repository.GetAlert(t.Context(), event.BKTenantID, processed.AlertIDs[0])
			if err != nil {
				t.Fatal(err)
			}
			message, err := kachook.CompatibilityMessage(alert.Alert, func(string) (string, error) { return "warning", nil })
			if err != nil || alert.Alert.EnrichStatus != domain.EnrichStatusSucceeded || message.ModelID != sample.Model || message.ModelInstID != sample.Instance.ID {
				t.Fatalf("resource replay: enrich=%s model_id=%s model_inst_id=%s want=%s/%s error=%v", alert.Alert.EnrichStatus, message.ModelID, message.ModelInstID, sample.Model, sample.Instance.ID, err)
			}
			if !reflect.DeepEqual(event, before) {
				t.Fatal("captured Event facts mutated")
			}
			t.Logf("captured Event %s: resource=succeeded KAC model_id=%s model_inst_id=%s", event.EventID, message.ModelID, message.ModelInstID)
		})
	}
}

type cmdbIdentityReplaySample struct {
	Event    domain.Event `json:"event"`
	Object   string       `json:"bk_obj_id"`
	Model    string       `json:"model_id"`
	Name     string       `json:"model_name"`
	Instance struct {
		Tenant     string         `json:"bk_tenant_id"`
		Model      string         `json:"model_id"`
		ID         string         `json:"model_inst_id"`
		UID        string         `json:"entity_uid"`
		Attributes map[string]any `json:"attributes"`
	} `json:"instance"`
}

type cmdbIdentityReplayReader struct{ sample cmdbIdentityReplaySample }

func (r cmdbIdentityReplayReader) GetModelByCode(_ context.Context, tenant, model string) (enrich.Model, bool, error) {
	return enrich.Model{TenantID: r.sample.Instance.Tenant, ModelID: "20", ModelCode: r.sample.Model,
		Fields: map[string]any{"object_model_name": r.sample.Name, "bk_cmdb_obj_id": r.sample.Object}}, tenant == r.sample.Instance.Tenant && model == r.sample.Model, nil
}

func (r cmdbIdentityReplayReader) GetModelByCMDBObjectID(ctx context.Context, tenant, object string) (enrich.Model, bool, error) {
	if object != r.sample.Object {
		return enrich.Model{}, false, nil
	}
	return r.GetModelByCode(ctx, tenant, r.sample.Model)
}

func (r cmdbIdentityReplayReader) FindInstance(_ context.Context, tenant string, query enrich.InstanceQuery) (enrich.Instance, bool, error) {
	i := r.sample.Instance
	if tenant != i.Tenant || query.ModelCode != i.Model {
		return enrich.Instance{}, false, nil
	}
	if query.InstanceID != "" && query.InstanceID != i.ID {
		return enrich.Instance{}, false, nil
	}
	if query.InstanceID == "" && len(query.AttributeFilters) == 0 {
		return enrich.Instance{}, false, enrich.ErrInvalidDataSourceResponse
	}
	// 快照 Reader 按实际存储属性判断旧查询；不得按请求凭空制造属性使回归通过。
	for _, filter := range query.AttributeFilters {
		value, found := i.Attributes[filter.Field]
		if !found || !replayEqualCMDBNumber(value, filter.Value) {
			return enrich.Instance{}, false, nil
		}
	}
	return enrich.Instance{TenantID: i.Tenant, ModelCode: i.Model, InstanceID: i.ID,
		Fields: map[string]any{"entity_uid": i.UID}, Attributes: i.Attributes}, true, nil
}

func replayEqualCMDBNumber(actual, expected any) bool {
	left, leftErr := json.Marshal(actual)
	right, rightErr := json.Marshal(expected)
	return leftErr == nil && rightErr == nil && string(left) == string(right)
}
