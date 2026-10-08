package datasources

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"linkd/internal/enrich/description"
)

type descriptionTxConnector struct {
	readerTestConnector
	t                     *testing.T
	committed, rolledBack *int
}

// 仅显式注入连接时执行；不发布配置、不消费 Kafka、不写业务数据。
func TestDescriptionConfigurationLiveIntegration(t *testing.T) {
	dsn := os.Getenv("LINKD_CONTENT_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("requires read-only integration database")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal("open integration database failed")
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal("integration database unavailable")
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Error("close integration database failed")
		}
	})
	client, err := NewDescriptionConfigurationClient(db)
	if err != nil {
		t.Fatal(err)
	}
	var rows []descriptionPublicationRow
	err = db.WithContext(t.Context()).Table("alarm_strategy_set_split_record").Select("id,bk_tenant_id,source_resource_version,bk_biz_ids,CASE WHEN OCTET_LENGTH(payload) <= ? THEN payload ELSE NULL END AS payload", maxDescriptionPublicationBytes).Where("bk_tenant_id = ? AND state = ? AND publish_status = ? AND parent_id IS NULL", os.Getenv("LINKD_CONTENT_TEST_TENANT"), "active", "published").Order("id").Limit(4).Find(&rows).Error
	if err != nil || len(rows) != 4 {
		t.Fatal("read four integration publication identities failed")
	}
	for _, row := range rows {
		var payload descriptionPublication
		var businesses []int64
		if err := json.Unmarshal(row.Payload, &payload); err != nil {
			var typeError *json.UnmarshalTypeError
			if errors.As(err, &typeError) {
				t.Fatalf("split=%d invalid publication field=%s expected=%s", row.ID, typeError.Field, typeError.Type)
			}
			t.Fatalf("split=%d invalid publication JSON", row.ID)
		}
		if json.Unmarshal(row.BusinessIDs, &businesses) != nil || len(businesses) == 0 {
			t.Fatal("invalid integration publication identity")
		}
		home := businesses[0]
		if payload.Set.ConfigType == "data" && len(businesses) > 1 && payload.Set.TemplateBusinessID != 0 {
			home = payload.Set.TemplateBusinessID
		}
		identity := description.ConfigurationQuery{TenantID: row.TenantID, StrategyID: row.ID, StrategyVersion: row.Version, BusinessID: home}
		configuration, err := client.ReadConfiguration(t.Context(), identity)
		if err != nil {
			var failure *description.Error
			if errors.As(err, &failure) {
				t.Fatalf("split=%d code=%s", row.ID, failure.Code)
			}
			t.Fatalf("split=%d dependency read failed", row.ID)
		}
		if configuration.Identity != identity || len(configuration.Queries) == 0 || len(configuration.Algorithms) == 0 {
			t.Fatalf("split=%d configuration facts incomplete", row.ID)
		}
		t.Logf("split=%d version=%d business=%d queries=%d algorithms=%d binding verified", row.ID, row.Version, home, len(configuration.Queries), len(configuration.Algorithms))
	}
}

func (c descriptionTxConnector) Connect(context.Context) (driver.Conn, error) {
	return descriptionTxConn{readerTestConn: readerTestConn(c.readerTestConnector), connector: c}, nil
}
func (c descriptionTxConnector) Driver() driver.Driver { return c }
func (c descriptionTxConnector) Open(string) (driver.Conn, error) {
	return c.Connect(context.Background())
}

type descriptionTxConn struct {
	readerTestConn
	connector descriptionTxConnector
}

func (c descriptionTxConn) BeginTx(ctx context.Context, options driver.TxOptions) (driver.Tx, error) {
	if !options.ReadOnly || options.Isolation != driver.IsolationLevel(sql.LevelRepeatableRead) {
		c.connector.t.Fatal("transaction must be read only repeatable read")
	}
	if _, ok := ctx.Deadline(); !ok {
		c.connector.t.Fatal("read deadline missing")
	}
	return descriptionTestTx{c.connector.committed, c.connector.rolledBack}, nil
}

type descriptionTestTx struct{ committed, rolledBack *int }

func (tx descriptionTestTx) Commit() error   { *tx.committed++; return nil }
func (tx descriptionTestTx) Rollback() error { *tx.rolledBack++; return nil }

const descriptionTestSetUID = "aabbccdd00112233445566778899aabb"
const descriptionTestConfigUID = "d98eb9bc9c704d9a820d2c7cb8c51eab"
const descriptionTestConfig = `{"id":"d98eb9bc-9c70-4d9a-820d-2c7cb8c51eab","enable":true,"inner_strategy_config":{"name":"CPU"}}`
const descriptionTestSpec = `{"enable":true,"name":"CPU","config_type":"data","strategy_item":{"agg_method":"AVG","query_configs":[{"unit":"percent"}]},"targets":[]}`
const descriptionTestCloudSpec = `{"enable":true,"name":"CPU","config_type":"data","cloud_id":42,"cloud_type":"aws","cloud_resource_type":"ec2","strategy_item":{"agg_method":"AVG","query_configs":[{"unit":"percent"}]},"targets":[]}`

func descriptionPublicationFixture(t *testing.T, businesses []int64, home int64) string {
	t.Helper()
	resolved := []any{}
	runtime := []any{}
	for _, business := range businesses {
		resolved = append(resolved, map[string]any{"metadata": map[string]any{"labels": map[string]any{"bk_tenant_id": "tenant", "bk_biz_id": business, "monitor_template_id": int64(7), "config_id": descriptionTestConfigUID, "is_default": true}}, "spec": json.RawMessage(descriptionTestSpec)})
		runtime = append(runtime, map[string]any{"query_configs": []any{map[string]any{"unit": "percent"}}, "algorithms": []any{map[string]any{"type": "Threshold", "level": 2, "unit_prefix": "%", "config": []any{[]any{map[string]any{"method": "gt", "threshold": 80}}}}}})
	}
	payload, err := json.Marshal(map[string]any{"schema_version": 1, "strategy_set": map[string]any{"uid": descriptionTestSetUID, "monitor_template_id": 7, "config_type": "data", "template_bk_biz_id": home}, "strategy_config": json.RawMessage(descriptionTestConfig), "resolved_strategies": resolved, "runtime_query_configs": runtime})
	if err != nil {
		t.Fatal(err)
	}
	return string(payload)
}

func TestDescriptionConfigurationTransactionAndBindings(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, code string
		businesses []int64
		home       int64
		change     string
	}{
		{"default", "", []int64{2}, 2, ""},
		{"cloud configuration exact frozen dependencies", "", []int64{2}, 2, "cloud"},
		{"cloud identity dependency changed", "configuration_config_changed", []int64{2}, 2, "cloud changed"},
		{"unknown configuration kind", "configuration_config_invalid", []int64{2}, 2, "unknown kind"},
		{"global data", "", []int64{2, 4}, 5, ""},
		{"target across businesses uses one resolved config", "", []int64{2, 4}, 5, "target"},
		{"target with multiple monitored items rejected", "publication_target_items_unverified", []int64{2, 4}, 5, "target multiple"},
		{"resolved tenant mismatch", "publication_binding_invalid", []int64{2}, 2, "resolved tenant"},
		{"observation business differs from template", "", []int64{524}, 524, "observed business"},
		{"config resource business differs from projection", "", []int64{524}, 524, "resource business"},
		{"duplicate default configs", "configuration_config_missing_or_ambiguous", []int64{2}, 2, "duplicate default"},
		{"equivalent defaults across businesses", "", []int64{2}, 2, "equivalent defaults"},
		{"different defaults across businesses", "configuration_config_changed", []int64{2}, 2, "different defaults"},
		{"default candidates exceed bound", "configuration_config_missing_or_ambiguous", []int64{2}, 2, "too many defaults"},
		{"version changed", "publication_version_mismatch", []int64{2}, 2, "version"},
		{"tenant mismatch", "publication_version_mismatch", []int64{2}, 2, "tenant"},
		{"override without immutable revision", "publication_override_revision_missing", []int64{2}, 2, "override"},
		{"set changed", "configuration_set_changed", []int64{2}, 2, "set"},
		{"config changed", "configuration_config_changed", []int64{2}, 2, "config"},
		{"targets do not render", "", []int64{2}, 2, "targets"},
		{"log theme query context does not render", "", []int64{2}, 2, "log theme"},
		{"dependency", "", []int64{2}, 2, "dependency"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			committed, rolledBack, queries := 0, 0, 0
			dependency := errors.New("database unavailable")
			query := func(ctx context.Context, statement string, args []driver.NamedValue) (driver.Rows, error) {
				queries++
				limit := int64(2)
				if strings.Contains(statement, "core_v1alpha1_strategy`") {
					limit = maxDescriptionResolvedConfigs + 1
				}
				if strings.Contains(statement, "history") || strings.Contains(statement, "revision") || !strings.Contains(statement, "bk_tenant_id") || !strings.Contains(statement, "OCTET_LENGTH") || args[1].Value != "tenant" || args[len(args)-1].Value != limit {
					t.Fatalf("unsafe query: %s", statement)
				}
				if tc.change == "dependency" {
					return nil, dependency
				}
				switch {
				case strings.Contains(statement, "alarm_strategy_set_split_record"):
					version, tenant := int64(1790758008036099), "tenant"
					var parent driver.Value
					if tc.change == "version" {
						version++
					}
					if tc.change == "tenant" {
						tenant = "other"
					}
					if tc.change == "override" {
						parent = int64(9)
					}
					businesses, _ := json.Marshal(tc.businesses)
					publication := descriptionPublicationFixture(t, tc.businesses, tc.home)
					if tc.change == "cloud" || tc.change == "cloud changed" {
						publication = strings.ReplaceAll(publication, descriptionTestSpec, descriptionTestCloudSpec)
					}
					if tc.change == "target" || tc.change == "target multiple" {
						publication = strings.ReplaceAll(publication, `"config_type":"data"`, `"config_type":"target"`)
						if tc.change == "target" {
							var payload map[string]any
							if err := json.Unmarshal([]byte(publication), &payload); err != nil {
								t.Fatal(err)
							}
							payload["resolved_strategies"] = payload["resolved_strategies"].([]any)[:1]
							payload["runtime_query_configs"] = payload["runtime_query_configs"].([]any)[:1]
							encoded, err := json.Marshal(payload)
							if err != nil {
								t.Fatal(err)
							}
							publication = string(encoded)
						}
					}
					if tc.change == "resolved tenant" {
						publication = strings.Replace(publication, `"bk_tenant_id":"tenant"`, `"bk_tenant_id":"other"`, 1)
					}
					return &readerTestRows{columns: []string{"id", "parent_id", "bk_tenant_id", "strategy_set_uid", "monitor_template_id", "config_uid", "source_resource_version", "state", "publish_status", "enabled", "bk_biz_ids", "payload"}, values: [][]driver.Value{{int64(1), parent, tenant, descriptionTestSetUID, int64(7), descriptionTestConfigUID, version, "active", "published", true, string(businesses), publication}}}, nil
				case strings.Contains(statement, "core_v1alpha1_strategyset"):
					config := descriptionTestConfig
					if tc.change == "set" {
						config = strings.Replace(config, "CPU", "changed", 1)
					}
					return &readerTestRows{columns: []string{"uid", "bk_tenant_id", "monitor_template_id", "kind", "api_version", "spec"}, values: [][]driver.Value{{descriptionTestSetUID, "tenant", int64(7), "StrategySet", "v1alpha1", `{"monitor_template_id":7,"strategy_configs":[` + config + `]}`}}}, nil
				case strings.Contains(statement, "core_v1alpha1_strategy"):
					if !strings.Contains(statement, "is_default") || strings.Contains(statement, "AND bk_biz_id") || args[3].Value != strings.ReplaceAll(descriptionTestConfigUID, "-", "") {
						t.Fatal("not bound to exact default configuration UUID")
					}
					spec := descriptionTestSpec
					kind := "Strategy"
					if tc.change == "cloud" || tc.change == "cloud changed" {
						spec, kind = descriptionTestCloudSpec, "StrategyCloud"
						if tc.change == "cloud changed" {
							spec = strings.Replace(spec, `"cloud_id":42`, `"cloud_id":43`, 1)
						}
					}
					if tc.change == "unknown kind" {
						kind = "Unknown"
					}
					if tc.change == "target" {
						spec = strings.ReplaceAll(spec, `"config_type":"data"`, `"config_type":"target"`)
					}
					if tc.change == "config" {
						spec = strings.Replace(spec, "AVG", "MAX", 1)
					}
					if tc.change == "targets" {
						spec = strings.Replace(spec, `"targets":[]`, `"targets":[{"enable":true}]`, 1)
					}
					if tc.change == "log theme" {
						spec = strings.Replace(spec, `"unit":"percent"`, `"unit":"percent","log_theme_list":[{"id":1}]`, 1)
					}
					business := tc.businesses[0]
					if tc.change == "resource business" {
						business = 3
					}
					values := [][]driver.Value{{"tenant", int64(7), descriptionTestConfigUID, business, kind, "v1alpha1", spec}}
					if tc.change == "duplicate default" {
						values = append(values, values[0])
					}
					if tc.change == "equivalent defaults" || tc.change == "different defaults" {
						other := append([]driver.Value(nil), values[0]...)
						other[3] = business + 1
						if tc.change == "different defaults" {
							other[6] = strings.Replace(spec, "CPU", "changed", 1)
						}
						values = append(values, other)
					}
					if tc.change == "too many defaults" {
						for len(values) <= maxDescriptionResolvedConfigs {
							other := append([]driver.Value(nil), values[0]...)
							other[3] = int64(len(values)) + business
							values = append(values, other)
						}
					}
					return &readerTestRows{columns: []string{"bk_tenant_id", "monitor_template_id", "config_id", "bk_biz_id", "kind", "api_version", "spec"}, values: values}, nil
				default:
					t.Fatalf("unexpected table: %s", statement)
					return nil, nil
				}
			}
			db := sql.OpenDB(descriptionTxConnector{readerTestConnector: readerTestConnector{query: query}, t: t, committed: &committed, rolledBack: &rolledBack})
			t.Cleanup(func() {
				if err := db.Close(); err != nil {
					t.Error(err)
				}
			})
			gormDB, err := gorm.Open(mysql.New(mysql.Config{Conn: db, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true, Logger: logger.Default.LogMode(logger.Silent)})
			if err != nil {
				t.Fatal(err)
			}
			client, err := NewDescriptionConfigurationClient(gormDB)
			if err != nil {
				t.Fatal(err)
			}
			identity := description.ConfigurationQuery{TenantID: "tenant", StrategyID: 1, StrategyVersion: 1790758008036099, BusinessID: tc.home}
			if tc.change == "observed business" {
				identity.BusinessID = 5
			}
			result, err := client.ReadConfiguration(t.Context(), identity)
			if tc.change == "dependency" {
				if !errors.Is(err, dependency) || rolledBack != 1 {
					t.Fatalf("err=%v rollback=%d", err, rolledBack)
				}
				return
			}
			if tc.code != "" {
				var failure *description.Error
				if !errors.As(err, &failure) || failure.Code != tc.code || rolledBack != 1 || committed != 0 {
					t.Fatalf("err=%v rollback=%d commit=%d", err, rolledBack, committed)
				}
				return
			}
			if err != nil || result.Identity != identity || result.Spec.Name != "CPU" || len(result.Algorithms) != 1 || queries != 3 || committed != 1 || rolledBack != 0 {
				t.Fatalf("err=%v queries=%d commit=%d rollback=%d", err, queries, committed, rolledBack)
			}
		})
	}
}
