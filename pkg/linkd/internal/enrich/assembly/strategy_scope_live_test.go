package assembly

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	driver "github.com/go-sql-driver/mysql"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/datasources"
	"linkd/internal/enrich/description"
	"linkd/internal/enrich/models"
	"linkd/internal/enrich/view"
	onemodelassembly "linkd/internal/onemodel/assembly"
)

// TestStrategySpaceLiveEnrich 显式选择环境，在只读 SQL 事务和 OneModel 上重放真实事件。
// 来源配置、事件与 Alert 由操作者导出；报告不包含原始载荷和凭据，不写线上告警或消息。
func TestStrategySpaceLiveEnrich(t *testing.T) {
	configJSON, folder := os.Getenv("LINKD_STRATEGY_SPACE_LIVE_CONFIG"), os.Getenv("LINKD_STRATEGY_SPACE_LIVE_FOLDER")
	if configJSON == "" || folder == "" {
		t.Skip("requires explicit environment and bounded captured samples")
	}
	var connection struct {
		MySQL    datasources.MySQLConfig
		OneModel config.OneModelResource
	}
	if json.Unmarshal([]byte(configJSON), &connection) != nil {
		t.Fatal("invalid live connection configuration")
	}
	read := func(name string, value any) {
		t.Helper()
		file := folder + "/" + name
		info, err := os.Stat(file) //nolint:gosec // G703: 操作者指定的集成测试目录，读取前限制大小。
		if err != nil || info.Size() > 8<<20 {
			t.Fatal("live fixture unavailable or oversized")
		}
		raw, err := os.ReadFile(file) //nolint:gosec // G304: 同上，不接受服务请求路径。
		if err != nil || json.Unmarshal(raw, value) != nil {
			t.Fatal("invalid live fixture")
		}
	}
	var samples []struct {
		Event   domain.Event `json:"event"`
		Opening domain.Alert `json:"opening"`
	}
	read("samples.json", &samples)
	var enrichConfig config.EnrichConfig
	read("enrich.json", &enrichConfig)
	if len(samples) == 0 || len(samples) > 32 {
		t.Fatal("invalid live sample count")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	cfg := driver.NewConfig()
	cfg.User = connection.MySQL.Username
	cfg.Passwd = connection.MySQL.Password
	cfg.Net = "tcp"
	cfg.Addr = connection.MySQL.Address
	cfg.DBName = connection.MySQL.Database
	cfg.ParseTime = true
	cfg.Timeout = 5 * time.Second
	cfg.ReadTimeout = 10 * time.Second
	cfg.WriteTimeout = 5 * time.Second
	sqlDB, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal("open live database failed")
	}
	defer func() {
		if err := sqlDB.Close(); err != nil {
			t.Error("close live database failed")
		}
	}()
	tx, err := sqlDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal("start read-only transaction failed")
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			t.Error("rollback live transaction failed")
		}
	}()
	db, err := gorm.Open(mysql.New(mysql.Config{Conn: tx, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true, Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal("initialize live database failed")
	}
	sources := enrich.Sources{}
	sources.CWStrategy, err = datasources.NewCWStrategyClient(datasources.CWStrategyClientConfig{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	sources.Metric, err = datasources.NewMetricClient(datasources.MetricClientConfig{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	sources.Model, err = datasources.NewModelClient(datasources.ModelClientConfig{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	sources.AlarmSource, err = datasources.NewAlarmSourceClient(datasources.AlarmSourceClientConfig{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	sources.DescriptionConfiguration, err = datasources.NewDescriptionConfigurationClient(db)
	if err != nil {
		t.Fatal(err)
	}
	client, transport, err := onemodelassembly.Open(&connection.OneModel, 4, 5*time.Second)
	if err != nil {
		t.Fatal("open live OneModel failed")
	}
	defer func() {
		if err := transport.Close(); err != nil {
			t.Error("close live OneModel failed")
		}
	}()
	sources.OneModel, sources.CMDB, sources.CollectTopology = client, client, client
	sources.K8s, err = datasources.NewOneModelK8sReader(client)
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := description.NewResolver(sources.DescriptionConfiguration)
	if err != nil {
		t.Fatal(err)
	}
	type record struct {
		Strategy      models.StrategyRevisionQuery
		Status        domain.EnrichStatus
		Processors    map[string]any
		Title         string
		Metric        models.MetricValues
		ContentBuilt  bool
		InstanceFound bool
	}
	records := make([]record, 0, len(samples))
	for _, sample := range samples {
		identity, diagnostics := enrich.ValidateStrategyIdentity(sample.Event)
		if len(diagnostics) != 0 || identity.ID != 147 {
			t.Fatal("fixture is not a valid strategy 147 event")
		}
		instance, instanceFound, instanceErr := sources.K8s.FindK8sInstance(ctx, sample.Event.BKTenantID, "cw-K8s_Container", sample.Event.Dimensions)
		if instanceErr != nil {
			t.Fatal("independent K8s lookup failed")
		}
		instanceFound = instanceFound && instance.TenantID == sample.Event.BKTenantID && instance.ModelCode == "cw-K8s_Container" && instance.InstanceID != ""
		original := sample.Event.Clone()
		opening := sample.Opening.Clone()
		source := baseCollectEventSource()
		source.RelatedTenantID = sample.Event.BKTenantID
		source.Version = sample.Event.EventSourceVersion
		source.EventSourceID = sample.Event.EventSourceID
		source.Enrich = enrichConfig
		router, err := NewRouter([]config.EventSource{source}, sources, WithDescriptionFacts(resolver))
		if err != nil {
			t.Fatal(err)
		}
		result, err := router.Enrich(ctx, enrich.Input{Event: sample.Event, Preview: true})
		if err != nil {
			t.Fatal(err)
		}
		row := record{Strategy: identity, Status: result.Status, InstanceFound: instanceFound, Processors: map[string]any{}}
		if len(result.Data.Evaluations) != 1 {
			t.Fatal("expected one triggered severity")
		}
		evaluation := result.Data.Evaluations[0]
		payload, err := enrich.DecodePayload(evaluation.Data)
		if err != nil {
			t.Fatal(err)
		}
		for _, entries := range payload.Processors {
			for name, output := range entries {
				row.Processors[name] = map[string]any{"status": output.Status, "diagnostics": output.Diagnostics}
				t.Logf("147/v%d processor=%s status=%s diagnostics=%v", identity.Version, name, output.Status, output.Diagnostics)
				expected := domain.EnrichStatusSucceeded
				if name == "apm" || name == "log" {
					expected = domain.EnrichStatusSkipped
				}
				if (name == "monitor-resource" || name == "k8s") && !instanceFound {
					expected = domain.EnrichStatusPartial
				}
				if output.Status != expected {
					t.Errorf("147/v%d processor %s status=%s want=%s", identity.Version, name, output.Status, expected)
				}
				if expected == domain.EnrichStatusPartial && (len(output.Diagnostics) != 1 || output.Diagnostics[0].Dependency != "onemodel") {
					t.Errorf("unexpected missing-instance diagnostics: %v", output.Diagnostics)
				}
				if name == "metric" {
					raw, _ := json.Marshal(output.Value)
					if json.Unmarshal(raw, &row.Metric) != nil {
						t.Fatal("invalid metric output")
					}
				}
			}
		}
		enriched := sample.Event.Clone()
		enriched.Enrich, enriched.EnrichStatus = result.Data, result.Status
		effective, err := view.EnrichedEvent(enriched, evaluation.Severity)
		if err != nil {
			t.Fatal(err)
		}
		row.Title = effective.Title
		if template, ok := effective.Labels["monitor_template_id"].NumberValue(); !ok || template != 172 {
			t.Error("monitor template not enriched")
		}
		if name, ok := effective.Labels["strategy_name"].StringValue(); !ok || name == "" {
			t.Error("strategy name not enriched")
		}
		if id, ok := effective.Labels["strategy_config_id"].StringValue(); !ok || id != "1259a648f4794ec68375dc66ab02d7d0" {
			t.Error("strategy config not enriched")
		}

		if effective.Title == "" || effective.Title == sample.Event.Title || row.Metric.MetricName != "container_memory_resource_limits_ratio" || row.Metric.Unit != "percent" {
			t.Errorf("147/v%d title or metric not enriched", identity.Version)
		}
		for _, candidate := range sample.Event.Evaluations {
			if candidate.Severity != evaluation.Severity {
				continue
			}
			content, err := router.BuildContent(ctx, sample.Event, candidate, sample.Opening)
			if err != nil {
				t.Errorf("147/v%d description failed: %v", identity.Version, err)
			}
			row.ContentBuilt = content != "" && err == nil
		}
		if !reflect.DeepEqual(original, sample.Event) || !reflect.DeepEqual(opening, sample.Opening) {
			t.Fatal("live input changed")
		}
		records = append(records, row)
	}
	raw, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	err = os.WriteFile(folder+"/report.json", raw, 0o600) //nolint:gosec // G703: 操作者指定目录，报告不含载荷和凭据。
	if err != nil {
		t.Fatal("write private live report failed")
	}
}
