package assembly

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	mysql "github.com/go-sql-driver/mysql"
	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/datasources"
	"linkd/internal/enrich/models"
	"linkd/internal/enrich/processors"
	"linkd/internal/enrich/rules"
	"linkd/internal/enrich/view"
	onemodelassembly "linkd/internal/onemodel/assembly"
)

// 只读线上发布记录、业务、指标和 OneModel，将丰富结果合成在内存中。
// 必须显式提供环境与有界 fixture；不修改告警、EventSource 或 Kafka offset。
func TestPublishedStrategyLiveTitle(t *testing.T) {
	dsn := os.Getenv("LINKD_PUBLISHED_STRATEGY_TEST_DSN")
	address := os.Getenv("LINKD_PUBLISHED_STRATEGY_TEST_ES_ADDRESS")
	file := os.Getenv("LINKD_PUBLISHED_STRATEGY_TEST_ALERT_FILE")
	if dsn == "" || address == "" || file == "" {
		t.Skip("requires explicit read-only MySQL, ES and Alert fixture")
	}
	info, err := os.Stat(file) //nolint:gosec // G703: 集成测试只读取操作者显式指定的本地 fixture，大小有硬上限。
	if err != nil || info.Size() > 1<<20 {
		t.Fatal("Alert fixture unavailable or oversized")
	}
	raw, err := os.ReadFile(file) //nolint:gosec // G304: 同上；文件不来自服务请求，且已检查大小。
	if err != nil {
		t.Fatal("read Alert fixture failed")
	}
	var fixture struct {
		Event              domain.Event `json:"event"`
		ExpectedTitle      string       `json:"expected_title"`
		ExpectedTemplateID int64        `json:"expected_template_id"`
	}
	if json.Unmarshal(raw, &fixture) != nil || fixture.ExpectedTitle == "" || fixture.ExpectedTemplateID <= 0 {
		t.Fatal("invalid Alert fixture")
	}
	databaseConfig, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal("invalid read-only database configuration")
	}
	runtime, err := datasources.Open(t.Context(), datasources.Config{MySQL: &datasources.MySQLConfig{Address: databaseConfig.Addr, Database: databaseConfig.DBName, Username: databaseConfig.User, Password: databaseConfig.Passwd}}, 4)
	if err != nil {
		t.Fatal("open read-only datasource failed")
	}
	t.Cleanup(func() {
		if err := runtime.Close(); err != nil {
			t.Error("close datasource failed")
		}
	})
	client, transport, err := onemodelassembly.Open(&config.OneModelResource{Addresses: []string{address}}, 4, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := transport.Close(); err != nil {
			t.Error(err)
		}
	})
	sources := runtime.Sources()
	ids, diagnostics := enrich.ValidateRequiredIDs(fixture.Event)
	if len(diagnostics) != 0 {
		t.Fatalf("source identity invalid: %v", diagnostics)
	}
	strategy, found, err := sources.CWStrategy.GetByStrategyID(t.Context(), models.StrategyQuery{TenantID: fixture.Event.BKTenantID, ID: ids.StrategyID, Version: ids.StrategyVersion})
	if err != nil || !found {
		t.Fatalf("published strategy found=%t error=%v", found, err)
	}
	if strategy.MonitorTemplateID == nil || *strategy.MonitorTemplateID != fixture.ExpectedTemplateID {
		t.Fatal("publication template does not match fixture")
	}
	sources.OneModel, sources.CollectTopology = client, client
	chain, err := enrich.NewChain([]enrich.Processor{processors.Strategy{}, processors.Resource{}, processors.Display{}, processors.Metric{}}, sources)
	if err != nil {
		t.Fatal(err)
	}
	result, err := chain.Enrich(t.Context(), enrich.Input{Event: fixture.Event, Preview: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Data.Evaluations) != 1 {
		t.Fatal("expected one source evaluation")
	}
	evaluation := result.Data.Evaluations[0]
	payload, err := enrich.DecodePayload(evaluation.Data)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range payload.Processors {
		for name, output := range entry {
			t.Logf("processor=%s status=%s diagnostics=%v", name, output.Status, output.Diagnostics)
			if (name == rules.StrategyProcessor || name == rules.DisplayProcessor || name == rules.MetricProcessor) && output.Status != domain.EnrichStatusSucceeded {
				t.Fatalf("processor %s did not succeed", name)
			}
		}
	}
	event := fixture.Event.Clone()
	event.Enrich, event.EnrichStatus = result.Data, result.Status
	effective, err := view.EnrichedEvent(event, evaluation.Severity)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"strategy_id", "strategy_version", "bk_biz_id"} {
		if effective.Labels[key] != fixture.Event.Labels[key] {
			t.Fatalf("source identity %s changed", key)
		}
	}
	if effective.Title != fixture.ExpectedTitle {
		t.Fatalf("title=%q want=%q", effective.Title, fixture.ExpectedTitle)
	}
	if value, ok := effective.Labels["monitor_template_id"].NumberValue(); !ok || value != float64(fixture.ExpectedTemplateID) {
		t.Fatalf("wrong monitor template: %v", effective.Labels["monitor_template_id"])
	}
	t.Logf("title=%q template=%d source_title=%q", effective.Title, fixture.ExpectedTemplateID, fixture.Event.Title)
}
