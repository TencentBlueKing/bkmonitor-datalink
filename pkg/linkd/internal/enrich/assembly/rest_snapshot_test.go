package assembly

import (
	_ "embed"
	"encoding/json"
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

//go:embed testdata/rest_missing_identity_snapshot.json
var restMissingIdentitySnapshot []byte

// 重放指定线上告警的两条原始输入，验证没有资源声明时不会猜测模型和实例。
// 正文脱敏，subject 与身份字段保留原样；全部处理、存储和 KAC 投影在内存执行。
func TestRESTCapturedAlertWithoutResourceIdentity(t *testing.T) {
	t.Parallel()
	var snapshot struct {
		Events []struct {
			EventID    string          `json:"event_id"`
			ReceivedAt time.Time       `json:"received_at"`
			Raw        json.RawMessage `json:"raw"`
		} `json:"events"`
	}
	if err := json.Unmarshal(restMissingIdentitySnapshot, &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Events) != 2 {
		t.Fatal("captured alert must contain its opening and latest events")
	}
	for _, sample := range snapshot.Events {
		t.Run(sample.EventID, func(t *testing.T) {
			source := config.EventSource{EventSourceID: "built_in_rest", Version: 2,
				Enrich: config.EnrichConfig{Processors: []config.EnrichProcessorConfig{{Type: "resource"}, {Type: "cmdb-access"}}},
			}
			factory, err := cleaner.NewNormalizationFactory(source, config.DefaultSeverityConfig())
			if err != nil {
				t.Fatal(err)
			}
			raw := cleaner.RawEventMessage{RecordID: sample.EventID, BKTenantID: "system", EventSourceID: source.EventSourceID, ReceivedAt: sample.ReceivedAt, Payload: sample.Raw}
			draft, err := (cleaner.StandardCleaner{}).Clean(t.Context(), raw)
			if err != nil {
				t.Fatal(err)
			}
			event, err := factory.Build(raw, draft)
			if err != nil {
				t.Fatal(err)
			}
			if event.SubjectSystem != "" || event.SubjectType != "" || event.SubjectID != "" || len(event.Labels)+len(event.Dimensions)+len(event.ExtraData) != 0 {
				t.Fatal("captured resource identity changed")
			}
			// 不注入资源目录或策略端口；任何错误的资源查询都会使此用例失败。
			router, err := NewRouter([]config.EventSource{source}, enrich.Sources{})
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
				t.Fatalf("outcome=%s alert count=%d error=%v", processed.Outcome, len(processed.AlertIDs), err)
			}
			storedAlert, err := repository.GetAlert(t.Context(), "system", processed.AlertIDs[0])
			if err != nil {
				t.Fatal(err)
			}
			alert := storedAlert.Alert
			payload, err := enrich.DecodePayload(alert.Enrich)
			if err != nil {
				t.Fatal(err)
			}
			resource := payload.Processors[0]["resource"]
			if resource.Status != domain.EnrichStatusSkipped || len(resource.Value) != 0 || payload.Processors[1]["cmdb-access"].Status != domain.EnrichStatusSucceeded {
				t.Fatalf("resource status=%s value=%v", resource.Status, resource.Value)
			}
			message, err := kachook.CompatibilityMessage(alert, func(string) (string, error) { return "warning", nil })
			if err != nil || message.ModelID != "" || message.ModelInstID != "" || len(message.CWLabels) != 0 || len(message.DynamicGroupID) != 0 {
				t.Fatalf("unexpected KAC resource: model=%q instance=%q error=%v", message.ModelID, message.ModelInstID, err)
			}
			t.Log("captured input: no resource identity; resource=skipped; KAC model and instance empty")
		})
	}
}
