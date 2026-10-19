// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package datasources

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"os"
	"strconv"
	"strings"
	"testing"

	"linkd/internal/enrich/description"
	"linkd/internal/enrich/models"
)

const revisionTestVersion int64 = 7
const revisionTestConfigUID = "d98eb9bc9c704d9a820d2c7cb8c51eab"

func revisionTestSnapshot(t *testing.T) map[string]any {
	t.Helper()
	return map[string]any{
		"id": int64(1), "bk_biz_id": int64(2), "bk_tenant_id": "tenant", "strategy_revision": revisionTestVersion,
		"runtime_config": map[string]any{
			"metadata": map[string]any{"uid": "00112233-4455-6677-8899-aabbccddeeff", "name": "7|config|2", "namespace": "default", "labels": map[string]any{
				"bk_tenant_id": "tenant", "bk_biz_id": int64(2), "monitor_template_id": int64(7), "config_id": revisionTestConfigUID, "is_default": true, "object_model_code": "cw-Host",
			}},
			"status": map[string]any{"bk_strategy_id": int64(1)},
			"spec":   map[string]any{"enable": true, "name": "CPU", "config_type": "data", "strategy_item": map[string]any{"agg_method": "AVG", "query_configs": []any{map[string]any{"unit": "percent", "metric_field": "usage", "result_table_id": "system.cpu", "agg_interval": 60}}}},
		},
		"items": []any{map[string]any{"query_configs": []any{map[string]any{"unit": "percent", "metric_id": "bk_monitor.system.cpu.usage", "metric_field": "usage", "result_table_id": "system.cpu"}}, "algorithms": []any{map[string]any{"type": "Threshold", "level": 2, "unit_prefix": "%", "config": []any{[]any{map[string]any{"method": "gt", "threshold": 80}}}}}}},
	}
}

func revisionStrategy(p map[string]any) map[string]any { return p }
func revisionRuntime(p map[string]any) map[string]any {
	return revisionStrategy(p)["runtime_config"].(map[string]any)
}
func revisionSpec(p map[string]any) map[string]any {
	return revisionRuntime(p)["spec"].(map[string]any)
}
func revisionLabels(p map[string]any) map[string]any {
	return revisionRuntime(p)["metadata"].(map[string]any)["labels"].(map[string]any)
}
func revisionItem(p map[string]any) map[string]any {
	return revisionStrategy(p)["items"].([]any)[0].(map[string]any)
}

func snapshotTestRows(t *testing.T, p map[string]any) *readerTestRows {
	t.Helper()
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	rows := revisionTestRows(raw)
	rows.values[0][1] = revisionStrategy(p)["bk_biz_id"]
	return rows
}

func assertRevisionQuery(t *testing.T, ctx context.Context, statement string, args []driver.NamedValue) {
	t.Helper()
	if !strings.Contains(statement, "FROM `alarm_strategy_execution_revision`") || !strings.Contains(statement, "bk_tenant_id = ? AND strategy_id = ? AND strategy_revision = ?") || !strings.Contains(statement, "strategy_bk_biz_id") || !strings.Contains(statement, "OCTET_LENGTH(snapshot_json)") || len(args) != 5 || args[0].Value != int64(maxStrategyRevisionBytes) || args[1].Value != "tenant" || args[2].Value != int64(1) || args[3].Value != revisionTestVersion || args[4].Value != int64(2) {
		t.Fatalf("unexpected history SQL: %s", statement)
	}
	if _, ok := ctx.Deadline(); !ok {
		t.Fatal("history read has no deadline")
	}
}

func TestDescriptionConfigurationReadsImmutableRevision(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, code string
		change     func(map[string]any)
	}{
		{name: "exact historical version without current tables"},
		{name: "compiled unit independent of declaration", change: func(p map[string]any) {
			revisionSpec(p)["strategy_item"].(map[string]any)["query_configs"].([]any)[0].(map[string]any)["unit"] = "declaration-only"
		}},
		{name: "global strategy ignores event business", change: func(p map[string]any) { revisionStrategy(p)["bk_biz_id"] = int64(5) }},
		{name: "override revision is immutable", change: func(p map[string]any) {
			revisionLabels(p)["is_default"] = false
			revisionSpec(p)["config_type"] = "target"
			revisionLabels(p)["bk_object_inst_id"] = 101
		}},
		{name: "unused runtime DTO", change: func(p map[string]any) {
			revisionItem(p)["query_configs"].([]any)[0].(map[string]any)["index_set_id"] = ""
		}},
		{name: "snapshot revision mismatch", code: "strategy_revision_identity_mismatch", change: func(p map[string]any) { p["strategy_revision"] = revisionTestVersion + 1 }},
		{name: "disabled spec", code: "configuration_disabled_or_invalid", change: func(p map[string]any) { revisionSpec(p)["enable"] = false }},
		{name: "missing runtime context", code: "strategy_revision_binding_invalid", change: func(p map[string]any) { delete(revisionStrategy(p), "runtime_config") }},
		{name: "snapshot strategy mismatch", code: "strategy_revision_identity_mismatch", change: func(p map[string]any) { revisionStrategy(p)["id"] = 9 }},
		{name: "runtime tenant mismatch", code: "strategy_revision_binding_invalid", change: func(p map[string]any) { revisionLabels(p)["bk_tenant_id"] = "other" }},
		{name: "runtime strategy mismatch", code: "strategy_revision_binding_invalid", change: func(p map[string]any) { revisionRuntime(p)["status"].(map[string]any)["bk_strategy_id"] = 9 }},
		{name: "multiple items", code: "strategy_revision_items_ambiguous", change: func(p map[string]any) { revisionStrategy(p)["items"] = []any{revisionItem(p), revisionItem(p)} }},
		{name: "empty items", code: "strategy_revision_items_invalid", change: func(p map[string]any) { revisionStrategy(p)["items"] = []any{} }},
		{name: "query budget", code: "strategy_revision_runtime_invalid", change: func(p map[string]any) { revisionItem(p)["query_configs"] = make([]any, 33) }},
		{name: "algorithm budget", code: "strategy_revision_runtime_invalid", change: func(p map[string]any) { revisionItem(p)["algorithms"] = make([]any, 33) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			db := openReaderTestDB(t, func(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
				calls++
				assertRevisionQuery(t, ctx, query, args)
				p := revisionTestSnapshot(t)
				if tc.change != nil {
					tc.change(p)
				}
				return snapshotTestRows(t, p), nil
			})
			client, err := NewDescriptionConfigurationClient(db)
			if err != nil {
				t.Fatal(err)
			}
			identity := description.ConfigurationQuery{TenantID: "tenant", StrategyID: 1, StrategyVersion: revisionTestVersion, BusinessID: 524}
			result, err := client.ReadConfiguration(t.Context(), identity)
			if calls != 1 {
				t.Fatalf("history reads=%d", calls)
			}
			if tc.code != "" {
				var failure *description.Error
				if !errors.As(err, &failure) || failure.Code != tc.code {
					t.Fatalf("error=%v want=%s", err, tc.code)
				}
				return
			}
			if err != nil || result.Identity != identity || result.Spec.Name != "CPU" || len(result.Algorithms) != 1 || result.Queries[0].Unit != "percent" {
				t.Fatalf("result=%+v error=%v", result, err)
			}
		})
	}
}

func TestStrategyReadsCancelInFlightSQL(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"enrich", "description"} {
		t.Run(name, func(t *testing.T) {
			started := make(chan struct{})
			db := openReaderTestDB(t, func(ctx context.Context, _ string, _ []driver.NamedValue) (driver.Rows, error) {
				close(started)
				<-ctx.Done()
				return nil, ctx.Err()
			})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			result := make(chan error, 1)
			go func() {
				if name == "description" {
					client, err := NewDescriptionConfigurationClient(db)
					if err != nil {
						result <- err
						return
					}
					_, err = client.ReadConfiguration(ctx, description.ConfigurationQuery{TenantID: "tenant", StrategyID: 1, StrategyVersion: 1, BusinessID: 2})
					result <- err
					return
				}
				client, err := NewCWStrategyClient(CWStrategyClientConfig{DB: db})
				if err != nil {
					result <- err
					return
				}
				_, _, err = client.GetByStrategyID(ctx, models.StrategyQuery{TenantID: "tenant", ID: 1, Version: 1})
				result <- err
			}()
			<-started
			cancel()
			if err := <-result; !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation lost: %v", err)
			}
		})
	}
}

func TestDescriptionConfigurationMissingDependencyAndCancellation(t *testing.T) {
	t.Parallel()
	dependency := errors.New("database unavailable")
	for _, tc := range []struct {
		name    string
		failure error
	}{{"missing", nil}, {"dependency", dependency}} {
		t.Run(tc.name, func(t *testing.T) {
			db := openReaderTestDB(t, func(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
				return &readerTestRows{columns: []string{"id"}}, tc.failure
			})
			client, err := NewDescriptionConfigurationClient(db)
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.ReadConfiguration(t.Context(), description.ConfigurationQuery{TenantID: "tenant", StrategyID: 1, StrategyVersion: 1, BusinessID: 2})
			if tc.failure != nil {
				if !errors.Is(err, dependency) {
					t.Fatal(err)
				}
			} else {
				var failure *description.Error
				if !errors.As(err, &failure) || failure.Code != "strategy_revision_missing" {
					t.Fatal(err)
				}
			}
		})
	}
	db := openReaderTestDB(t, func(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
		t.Fatal("invalid input queried database")
		return nil, nil
	})
	client, err := NewDescriptionConfigurationClient(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	identity := description.ConfigurationQuery{TenantID: "tenant", StrategyID: 1, StrategyVersion: 1, BusinessID: 2}
	if _, err := client.ReadConfiguration(ctx, identity); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := client.ReadConfiguration(nil, identity); err == nil { //nolint:staticcheck // SA1012: 验证调用方误传 nil Context 时明确拒绝，且不执行 SQL。
		t.Fatal("nil context accepted")
	}
	identity.TenantID = " tenant"
	if _, err := client.ReadConfiguration(t.Context(), identity); err == nil {
		t.Fatal("invalid tenant accepted")
	}
	if _, err := NewDescriptionConfigurationClient(nil); err == nil {
		t.Fatal("nil database accepted")
	}
}

// 显式注入只读连接与历史身份才执行；不枚举当前策略，不写配置或业务记录。
func TestDescriptionConfigurationLiveIntegration(t *testing.T) {
	dsn, tenant := os.Getenv("LINKD_CONTENT_TEST_DSN"), os.Getenv("LINKD_CONTENT_TEST_TENANT")
	id, idErr := strconv.ParseInt(os.Getenv("LINKD_CONTENT_TEST_STRATEGY_ID"), 10, 64)
	version, versionErr := strconv.ParseInt(os.Getenv("LINKD_CONTENT_TEST_STRATEGY_VERSION"), 10, 64)
	if dsn == "" || tenant == "" || idErr != nil || versionErr != nil || id <= 0 || version <= 0 {
		t.Skip("requires explicit read-only MySQL and exact strategy revision identity")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal("open read-only database failed")
	}
	connection, err := db.DB()
	if err != nil {
		t.Fatal("get database connection failed")
	}
	t.Cleanup(func() {
		if err := connection.Close(); err != nil {
			t.Error("close database failed")
		}
	})
	err = db.WithContext(t.Context()).Transaction(func(tx *gorm.DB) error {
		client, err := NewDescriptionConfigurationClient(tx)
		if err != nil {
			return err
		}
		result, err := client.ReadConfiguration(t.Context(), description.ConfigurationQuery{TenantID: tenant, StrategyID: id, StrategyVersion: version, BusinessID: 1})
		if err != nil {
			return err
		}
		strategyClient, err := NewCWStrategyClient(CWStrategyClientConfig{DB: tx})
		if err != nil {
			return err
		}
		strategy, found, err := strategyClient.GetByStrategyID(t.Context(), models.StrategyQuery{TenantID: tenant, ID: id, Version: version})
		if err != nil {
			return err
		}
		if !found || len(result.Queries) == 0 || len(result.Algorithms) == 0 || result.Spec.Name != strategy.Spec.Name {
			t.Fatal("historical strategy/query/algorithm facts missing or inconsistent")
		}
		if expected := os.Getenv("LINKD_CONTENT_TEST_EXPECTED_NAME"); expected != "" && result.Spec.Name != expected {
			t.Fatal("reader substituted a different execution revision")
		}
		return nil
	}, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
}
