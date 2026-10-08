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
	"strconv"
	"strings"
	"testing"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"linkd/internal/enrich"
)

const strategySetTestSpec = `{"monitor_template_id":7,"strategy_configs":[{"id":"d98eb9bc-9c70-4d9a-820d-2c7cb8c51eab","enable":true,"algorithms":{"deadly":[]},"inner_strategy_config":{"name":"CPU"},"inner_strategy_item":{"query_configs":[]},"inner_metric_info":[]}]}`

func TestStrategySetClientExactTenantAndTemplate(t *testing.T) {
	t.Parallel()
	db := openReaderTestDB(t, func(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
		if ctx != t.Context() || !strings.Contains(query, "core_v1alpha1_strategyset") || strings.Contains(query, "history") || !strings.Contains(query, "OCTET_LENGTH") {
			t.Fatalf("unexpected query or context: %s", query)
		}
		if len(args) != 5 || args[0].Value != int64(maxStrategySetSpecBytes) || args[1].Value != "tenant-1" || args[2].Value != int64(7) || args[3].Value != true || args[4].Value != int64(2) {
			t.Fatalf("query identity/bounds: %v", args)
		}
		return strategySetRows("tenant-1", strategySetTestSpec), nil
	})
	client, err := NewStrategySetClient(db)
	if err != nil {
		t.Fatal(err)
	}
	set, found, err := client.GetStrategySet(t.Context(), "tenant-1", 7)
	if err != nil || !found {
		t.Fatalf("found=%t err=%v", found, err)
	}
	config, found, err := set.Config("d98eb9bc9c704d9a820d2c7cb8c51eab")
	if err != nil || !found || config.InnerStrategyConfig.Name != "CPU" {
		t.Fatalf("config=%v found=%t err=%v", config, found, err)
	}
}

func strategySetRows(tenant, spec string) *readerTestRows {
	return &readerTestRows{columns: []string{"uid", "bk_tenant_id", "monitor_template_id", "kind", "api_version", "spec"}, values: [][]driver.Value{{"set-1", tenant, int64(7), "StrategySet", "v1alpha1", spec}}}
}

func TestStrategySetClientRejectsInvalidResponses(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, tenant, spec string
		duplicate          bool
	}{
		{name: "cross tenant", tenant: "tenant-2", spec: strategySetTestSpec},
		{name: "cross template", tenant: "tenant-1", spec: `{"monitor_template_id":8}`},
		{name: "invalid JSON", tenant: "tenant-1", spec: `{`},
		{name: "oversized or missing", tenant: "tenant-1", spec: ""},
		{name: "duplicate active", tenant: "tenant-1", spec: strategySetTestSpec, duplicate: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openReaderTestDB(t, func(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
				rows := strategySetRows(tc.tenant, tc.spec)
				if tc.duplicate {
					rows.values = append(rows.values, rows.values[0])
				}
				return rows, nil
			})
			client, err := NewStrategySetClient(db)
			if err != nil {
				t.Fatal(err)
			}
			_, found, err := client.GetStrategySet(t.Context(), "tenant-1", 7)
			if found || !errors.Is(err, enrich.ErrInvalidDataSourceResponse) {
				t.Fatalf("found=%t err=%v", found, err)
			}
		})
	}
}

func TestStrategySetClientMissingAndDependencyFailure(t *testing.T) {
	t.Parallel()
	dependency := errors.New("database unavailable")
	for _, tc := range []struct {
		name string
		err  error
	}{{"missing", nil}, {"dependency", dependency}} {
		t.Run(tc.name, func(t *testing.T) {
			db := openReaderTestDB(t, func(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
				return &readerTestRows{columns: []string{"uid"}}, tc.err
			})
			client, err := NewStrategySetClient(db)
			if err != nil {
				t.Fatal(err)
			}
			_, found, err := client.GetStrategySet(t.Context(), "tenant-1", 7)
			if found || !errors.Is(err, tc.err) {
				t.Fatalf("found=%t err=%v", found, err)
			}
		})
	}
}

func TestStrategySetClientCancellation(t *testing.T) {
	t.Parallel()
	db := openReaderTestDB(t, func(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
		t.Fatal("canceled input queried database")
		return nil, nil
	})
	client, err := NewStrategySetClient(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := client.GetStrategySet(ctx, "tenant-1", 7); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}

func TestStrategySetClientRejectsInvalidInput(t *testing.T) {
	t.Parallel()
	if _, err := NewStrategySetClient(nil); err == nil {
		t.Fatal("nil database accepted")
	}
	db := openReaderTestDB(t, func(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
		t.Fatal("invalid input queried database")
		return nil, nil
	})
	client, err := NewStrategySetClient(db)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		ctx      context.Context
		tenant   string
		template int64
	}{
		{"nil context", nil, "tenant-1", 7},
		{"empty tenant", t.Context(), "", 7},
		{"tenant whitespace", t.Context(), " tenant-1", 7},
		{"invalid template", t.Context(), "tenant-1", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, found, err := client.GetStrategySet(tc.ctx, tc.tenant, tc.template); found || err == nil {
				t.Fatalf("found=%t err=%v", found, err)
			}
		})
	}
}

func TestStrategySetClientBoundsAndSchema(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		mutate func(*readerTestRows)
	}{
		{"wrong kind", func(r *readerTestRows) { r.values[0][3] = "Strategy" }},
		{"wrong schema", func(r *readerTestRows) { r.values[0][4] = "v2" }},
		{"missing UID", func(r *readerTestRows) { r.values[0][0] = "" }},
		{"wrong row template", func(r *readerTestRows) { r.values[0][2] = int64(8) }},
		{"too many configurations", func(r *readerTestRows) {
			configs := make([]map[string]string, maxStrategySetConfigs+1)
			for i := range configs {
				configs[i] = map[string]string{"id": "d98eb9bc9c704d9a820d2c7cb8c51eab"}
			}
			data, err := json.Marshal(map[string]any{"monitor_template_id": 7, "strategy_configs": configs})
			if err != nil {
				t.Fatal(err)
			}
			r.values[0][5] = string(data)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openReaderTestDB(t, func(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
				rows := strategySetRows("tenant-1", strategySetTestSpec)
				tc.mutate(rows)
				return rows, nil
			})
			client, err := NewStrategySetClient(db)
			if err != nil {
				t.Fatal(err)
			}
			if _, found, err := client.GetStrategySet(t.Context(), "tenant-1", 7); found || !errors.Is(err, enrich.ErrInvalidDataSourceResponse) {
				t.Fatalf("found=%t err=%v", found, err)
			}
		})
	}
}

// TestStrategySetClientLiveIntegration 只读指定环境，普通门禁不隐式连接外部服务。
func TestStrategySetClientLiveIntegration(t *testing.T) {
	dsn := os.Getenv("LINKD_CONTENT_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("LINKD_CONTENT_TEST_MYSQL_DSN not set")
	}
	template, err := strconv.ParseInt(os.Getenv("LINKD_CONTENT_TEST_TEMPLATE_ID"), 10, 64)
	if err != nil {
		t.Fatal("valid template ID required")
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
			t.Error(err)
		}
	})
	client, err := NewStrategySetClient(db)
	if err != nil {
		t.Fatal(err)
	}
	set, found, err := client.GetStrategySet(t.Context(), os.Getenv("LINKD_CONTENT_TEST_TENANT"), template)
	if err != nil || !found {
		t.Fatalf("strategy set found=%t err=%v", found, err)
	}
	if _, found, err := set.Config(os.Getenv("LINKD_CONTENT_TEST_CONFIG_ID")); err != nil || !found {
		t.Fatalf("linked config found=%t err=%v", found, err)
	}
	if _, found, err := client.GetStrategySet(t.Context(), "linkd-nonexistent-tenant", template); err != nil || found {
		t.Fatalf("cross tenant found=%t err=%v", found, err)
	}
}
