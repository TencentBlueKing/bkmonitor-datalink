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
	"linkd/internal/enrich/models"
)

const splitTestSetUID = "aabbccdd00112233445566778899aabb"

const splitTestConfigUID = "d98eb9bc9c704d9a820d2c7cb8c51eab"

const splitTestVersion int64 = 1790758008036099

func splitTestPublication(t *testing.T, businesses ...int64) map[string]any {
	t.Helper()
	resolved, runtime := []any{}, []any{}
	for _, business := range businesses {
		resolved = append(resolved, map[string]any{
			"metadata": map[string]any{"uid": "00112233-4455-6677-8899-aabbccddeeff", "name": "7|config|2", "namespace": "default", "labels": map[string]any{
				"bk_tenant_id": "tenant", "bk_biz_id": business, "monitor_template_id": 7, "config_id": splitTestConfigUID, "is_default": true, "object_model_code": "cw-Host",
			}},
			"spec": map[string]any{"enable": true, "name": "CPU", "config_type": "data", "strategy_item": map[string]any{"agg_method": "AVG", "query_configs": []any{map[string]any{"unit": "percent", "metric_field": "usage", "result_table_id": "system.cpu", "agg_interval": 60}}}},
		})
		runtime = append(runtime, map[string]any{"query_configs": []any{map[string]any{"unit": "percent", "metric_id": "bk_monitor.system.cpu.usage"}}, "algorithms": []any{map[string]any{"type": "Threshold", "level": 2, "unit_prefix": "%", "config": []any{[]any{map[string]any{"method": "gt", "threshold": 80}}}}}})
	}
	return map[string]any{
		"schema_version":      1,
		"strategy_set":        map[string]any{"uid": splitTestSetUID, "monitor_template_id": 7, "config_type": "data", "template_bk_biz_id": 5},
		"strategy_config":     map[string]any{"id": splitTestConfigUID, "enable": true, "inner_strategy_config": map[string]any{"name": "CPU"}},
		"resolved_strategies": resolved, "runtime_query_configs": runtime,
	}
}

func splitTestRows(t *testing.T, payload map[string]any) *readerTestRows {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	businesses := []int64{}
	for _, entry := range payload["resolved_strategies"].([]any) {
		labels := entry.(map[string]any)["metadata"].(map[string]any)["labels"].(map[string]any)
		businesses = append(businesses, labels["bk_biz_id"].(int64))
	}
	bizJSON, err := json.Marshal(businesses)
	if err != nil {
		t.Fatal(err)
	}
	return &readerTestRows{columns: []string{"id", "parent_id", "bk_tenant_id", "strategy_set_uid", "monitor_template_id", "config_uid", "source_resource_version", "state", "publish_status", "enabled", "bk_biz_ids", "payload"}, values: [][]driver.Value{{int64(1), nil, "tenant", splitTestSetUID, int64(7), splitTestConfigUID, splitTestVersion, "active", "published", true, string(bizJSON), string(raw)}}}
}

func assertSplitQuery(t *testing.T, ctx context.Context, statement string, args []driver.NamedValue) {
	t.Helper()
	if !strings.Contains(statement, "FROM `alarm_strategy_set_split_record`") || !strings.Contains(statement, "bk_tenant_id = ? AND id = ? AND source_resource_version = ?") || !strings.Contains(statement, "OCTET_LENGTH(payload)") || len(args) != 5 || args[0].Value != int64(maxStrategyPublicationBytes) || args[1].Value != "tenant" || args[2].Value != int64(1) || args[3].Value != splitTestVersion || args[4].Value != int64(2) {
		t.Fatalf("unexpected or unbounded strategy query: %s", statement)
	}
	if _, ok := ctx.Deadline(); !ok {
		t.Fatal("strategy read has no deadline")
	}
}

func TestDescriptionConfigurationReadsOnlySplitRecord(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, code string
		businesses []int64
		change     func(map[string]any, *readerTestRows)
	}{
		{name: "default without legacy tables", businesses: []int64{2}},
		{name: "same spec across businesses", businesses: []int64{2, 4}},
		{name: "target multiple businesses one item", businesses: []int64{2}, change: func(p map[string]any, r *readerTestRows) {
			p["strategy_set"].(map[string]any)["config_type"] = "target"
			p["resolved_strategies"].([]any)[0].(map[string]any)["spec"].(map[string]any)["config_type"] = "target"
			r.values[0][10] = "[2,4]"
		}},
		{name: "version mismatch", code: "publication_version_mismatch", businesses: []int64{2}, change: func(_ map[string]any, r *readerTestRows) { r.values[0][6] = splitTestVersion + 1 }},
		{name: "tenant mismatch", code: "publication_version_mismatch", businesses: []int64{2}, change: func(_ map[string]any, r *readerTestRows) { r.values[0][2] = "other" }},
		{name: "wrong split identity", code: "publication_version_mismatch", businesses: []int64{2}, change: func(_ map[string]any, r *readerTestRows) { r.values[0][0] = int64(9) }},
		{name: "override revision unproven", code: "publication_override_revision_missing", businesses: []int64{2}, change: func(_ map[string]any, r *readerTestRows) { r.values[0][1] = int64(9) }},
		{name: "not published", code: "publication_not_active", businesses: []int64{2}, change: func(_ map[string]any, r *readerTestRows) { r.values[0][8] = "pending" }},
		{name: "disabled row", code: "publication_not_active", businesses: []int64{2}, change: func(_ map[string]any, r *readerTestRows) { r.values[0][9] = false }},
		{name: "deleted row", code: "publication_not_active", businesses: []int64{2}, change: func(_ map[string]any, r *readerTestRows) { r.values[0][7] = "deleted" }},
		{name: "set identity", code: "publication_binding_invalid", businesses: []int64{2}, change: func(p map[string]any, _ *readerTestRows) {
			p["strategy_set"].(map[string]any)["uid"] = splitTestConfigUID
		}},
		{name: "config identity", code: "publication_binding_invalid", businesses: []int64{2}, change: func(p map[string]any, _ *readerTestRows) {
			p["strategy_config"].(map[string]any)["id"] = splitTestSetUID
		}},
		{name: "resolved tenant", code: "publication_binding_invalid", businesses: []int64{2}, change: func(p map[string]any, _ *readerTestRows) { splitTestLabels(p)["bk_tenant_id"] = "other" }},
		{name: "compiled error", code: "publication_payload_invalid", businesses: []int64{2}, change: func(p map[string]any, _ *readerTestRows) { p["compile_error"] = "rejected" }},
		{name: "waiting metric binding", code: "publication_payload_invalid", businesses: []int64{2}, change: func(p map[string]any, _ *readerTestRows) { p["execution_state"] = "waiting_binding" }},
		{name: "different business specs", code: "publication_business_specs_differ", businesses: []int64{2, 4}, change: func(p map[string]any, _ *readerTestRows) {
			p["resolved_strategies"].([]any)[1].(map[string]any)["spec"].(map[string]any)["name"] = "different"
		}},
		{name: "missing business projection", code: "publication_business_mismatch", businesses: []int64{2}, change: func(_ map[string]any, r *readerTestRows) { r.values[0][10] = "[2,4]" }},
		{name: "runtime count mismatch", code: "publication_runtime_invalid", businesses: []int64{2}, change: func(p map[string]any, _ *readerTestRows) { p["runtime_query_configs"] = []any{} }},
		{name: "runtime query DTO invalid", code: "publication_runtime_invalid", businesses: []int64{2}, change: func(p map[string]any, _ *readerTestRows) {
			p["runtime_query_configs"].([]any)[0].(map[string]any)["query_configs"].([]any)[0].(map[string]any)["index_set_id"] = ""
		}},
		{name: "disabled frozen config remains rejected for content", code: "configuration_disabled_or_invalid", businesses: []int64{2}, change: func(p map[string]any, _ *readerTestRows) {
			p["strategy_config"].(map[string]any)["enable"] = false
		}},
		{name: "disabled frozen spec remains rejected for content", code: "configuration_disabled_or_invalid", businesses: []int64{2}, change: func(p map[string]any, _ *readerTestRows) {
			p["resolved_strategies"].([]any)[0].(map[string]any)["spec"].(map[string]any)["enable"] = false
		}},
		{name: "runtime error", code: "publication_runtime_invalid", businesses: []int64{2}, change: func(p map[string]any, _ *readerTestRows) {
			p["runtime_query_configs"].([]any)[0].(map[string]any)["error"] = "invalid"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			db := openReaderTestDB(t, func(ctx context.Context, statement string, args []driver.NamedValue) (driver.Rows, error) {
				calls++
				assertSplitQuery(t, ctx, statement, args)
				payload := splitTestPublication(t, tc.businesses...)
				rows := splitTestRows(t, payload)
				if tc.change != nil {
					tc.change(payload, rows)
				}
				raw, err := json.Marshal(payload)
				if err != nil {
					t.Fatal(err)
				}
				rows.values[0][11] = string(raw)
				return rows, nil
			})
			client, err := NewDescriptionConfigurationClient(db)
			if err != nil {
				t.Fatal(err)
			}
			identity := description.ConfigurationQuery{TenantID: "tenant", StrategyID: 1, StrategyVersion: splitTestVersion, BusinessID: 524}
			result, err := client.ReadConfiguration(t.Context(), identity)
			if calls != 1 {
				t.Fatalf("publication required %d SQL reads", calls)
			}
			if tc.code != "" {
				var failure *description.Error
				if !errors.As(err, &failure) || failure.Code != tc.code {
					t.Fatalf("got %v want %s", err, tc.code)
				}
				return
			}
			if err != nil || result.Identity != identity || result.Spec.Name != "CPU" || len(result.Algorithms) != 1 || result.Queries[0].Unit != "percent" {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}
}

func splitTestLabels(p map[string]any) map[string]any {
	return p["resolved_strategies"].([]any)[0].(map[string]any)["metadata"].(map[string]any)["labels"].(map[string]any)
}

func TestStrategyPublicationBudgets(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, code     string
		count          int
		runtimeQueries int
	}{
		{"maximum resolved configs", "", 32, 1},
		{"resolved configs exceed budget", "publication_runtime_invalid", 33, 1},
		{"queries exceed budget", "publication_runtime_invalid", 1, 33},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openReaderTestDB(t, func(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
				businesses := make([]int64, tc.count)
				for i := range businesses {
					businesses[i] = int64(i + 1)
				}
				payload := splitTestPublication(t, businesses...)
				runtime := payload["runtime_query_configs"].([]any)[0].(map[string]any)
				queries := make([]any, tc.runtimeQueries)
				for i := range queries {
					queries[i] = map[string]any{"unit": "percent"}
				}
				runtime["query_configs"] = queries
				return splitTestRows(t, payload), nil
			})
			client, err := NewDescriptionConfigurationClient(db)
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.ReadConfiguration(t.Context(), description.ConfigurationQuery{TenantID: "tenant", StrategyID: 1, StrategyVersion: splitTestVersion, BusinessID: 2})
			if tc.code == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var failure *description.Error
			if !errors.As(err, &failure) || failure.Code != tc.code {
				t.Fatalf("got %v want %s", err, tc.code)
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
				if !errors.As(err, &failure) || failure.Code != "publication_missing_or_ambiguous" {
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

// TestDescriptionConfigurationLiveIntegration 仅显式注入连接时读取，不写配置或业务数据。
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
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Error("close integration database failed")
		}
	})
	var rows []strategyPublicationRow
	err = db.WithContext(t.Context()).Table("alarm_strategy_set_split_record").Select("id,bk_tenant_id,source_resource_version,bk_biz_ids").Where("bk_tenant_id = ? AND state = ? AND publish_status = ? AND parent_id IS NULL", os.Getenv("LINKD_CONTENT_TEST_TENANT"), "active", "published").Order("id").Limit(4).Find(&rows).Error
	if err != nil || len(rows) == 0 {
		t.Fatal("read integration publication identities failed")
	}
	client, err := NewDescriptionConfigurationClient(db)
	if err != nil {
		t.Fatal(err)
	}
	strategyClient, err := NewCWStrategyClient(CWStrategyClientConfig{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		var businesses []int64
		if json.Unmarshal(row.BusinessIDs, &businesses) != nil || len(businesses) == 0 {
			t.Fatal("invalid integration business identity")
		}
		query := description.ConfigurationQuery{TenantID: row.TenantID, StrategyID: row.ID, StrategyVersion: row.Version, BusinessID: businesses[0]}
		result, err := client.ReadConfiguration(t.Context(), query)
		if err != nil {
			t.Fatalf("split=%d configuration failed: %v", row.ID, err)
		}
		strategy, found, err := strategyClient.GetByStrategyID(t.Context(), models.StrategyQuery{TenantID: row.TenantID, ID: row.ID, Version: row.Version})
		if err != nil || !found || result.Spec.Name != strategy.Spec.Name {
			t.Fatalf("split=%d readers disagree: %v", row.ID, err)
		}
	}
}
