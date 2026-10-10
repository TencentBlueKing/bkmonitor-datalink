// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Tencent is pleased to support the open source community. All rights reserved.
// Licensed under the MIT License.

package datasources

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"linkd/internal/enrich"
	"linkd/internal/enrich/models"
)

func metricTestColumns() []string {
	return []string{"id", "bk_tenant_id", "space_uid", "model_id", "metric_name", "display_name", "description", "unit", "kind", "result_table_id", "data_label", "physical_field", "value_mapping", "dimensions"}
}

func metricTestRow() []driver.Value {
	return []driver.Value{int64(42), "tenant-a", "*", "cw-Host", "usage", "CPU 使用率", "CPU usage", "percent", "native", "system.cpu", "plugin.cpu", "usage", []byte(`[{"original_value":"1","mapped_value":"正常"}]`), []byte(`[{"id":"bk_target_ip","name":"目标IP","src_name":"IP","detect":true}]`)}
}

func TestMetricClientCatalogProjection(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		query     models.MetricQuery
		fragments []string
		args      []any
	}{
		{name: "physical", query: models.MetricQuery{TenantID: "tenant-a", SpaceUID: "bkcc__2", TableID: "system.cpu", FieldName: "usage", ObjectModelCode: "cw-Host"}, fragments: []string{"space_uid IN (?,?)", "kind = ? AND physical_field = ? AND (result_table_id = ? OR data_label = ?)", "model_id = ?"}, args: []any{int64(maxMetricJSONBytes), int64(maxMetricJSONBytes), "tenant-a", "bkcc__2", "*", "native", "usage", "system.cpu", "system.cpu", "cw-Host"}},
		{name: "alias", query: models.MetricQuery{TenantID: "tenant-a", TableID: "plugin.cpu", FieldName: "usage"}, fragments: []string{"result_table_id = ? OR data_label = ?"}, args: []any{int64(maxMetricJSONBytes), int64(maxMetricJSONBytes), "tenant-a", "native", "usage", "plugin.cpu", "plugin.cpu"}},
		{name: "ID dominates stale location and model", query: models.MetricQuery{TenantID: "tenant-a", MetricID: 42, TableID: "stale", FieldName: "stale", ObjectModelCode: "stale", SpaceUID: "bkcc__2"}, fragments: []string{"bk_tenant_id = ? AND id = ?"}, args: []any{int64(maxMetricJSONBytes), int64(maxMetricJSONBytes), "tenant-a", int64(42)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			calls := 0
			db := openReaderTestDB(t, func(ctx context.Context, sql string, args []driver.NamedValue) (driver.Rows, error) {
				calls++
				sql = strings.ReplaceAll(sql, "`", "")
				if !strings.Contains(sql, "FROM metric WHERE") || !strings.Contains(sql, "LIMIT ?") {
					t.Errorf("unbounded or wrong table: %s", sql)
				}
				for _, fragment := range tc.fragments {
					if !strings.Contains(sql, fragment) {
						t.Errorf("missing %q: %s", fragment, sql)
					}
				}
				values := make([]any, 0, len(args))
				for _, arg := range args {
					values = append(values, arg.Value)
				}
				want := append(append([]any{}, tc.args...), int64(2))
				if !reflect.DeepEqual(values, want) {
					t.Errorf("args=%#v want=%#v", values, want)
				}
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > 5*time.Second {
					t.Error("read must have timeout")
				}
				return &readerTestRows{columns: metricTestColumns(), values: [][]driver.Value{metricTestRow()}}, nil
			})
			client, _ := NewMetricClient(MetricClientConfig{DB: db})
			got, found, err := client.FindMetric(context.Background(), tc.query)
			if err != nil || !found || calls != 1 || got.FieldCNName != "CPU 使用率" || got.FieldName != "usage" || got.Unit != "percent" || got.ObjectModelCode != "cw-Host" || len(got.Dimensions) != 1 || got.Dimensions[0].Key != "bk_target_ip" || got.Dimensions[0].Name != "目标IP" || len(got.ValueMapping) != 1 || got.ValueMapping[0].MappedValue != "正常" {
				t.Fatalf("projection=%#v found=%v err=%v calls=%d", got, found, err, calls)
			}
		})
	}
}

func TestMetricClientFailureAndMissing(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		modify   func([]driver.Value)
		count    int
		queryErr error
		wantErr  bool
	}{
		{name: "missing ID has no fallback", count: 0},
		{name: "ambiguous", count: 2, wantErr: true},
		{name: "query failure", queryErr: errors.New("database unavailable"), wantErr: true},
		{name: "scan failure", count: 1, modify: func(row []driver.Value) { row[0] = "not-int" }, wantErr: true},
		{name: "other tenant", count: 1, modify: func(row []driver.Value) { row[1] = "tenant-b" }, wantErr: true},
		{name: "wrong ID", count: 1, modify: func(row []driver.Value) { row[0] = int64(43) }, wantErr: true},
		{name: "empty name", count: 1, modify: func(row []driver.Value) { row[4] = "" }, wantErr: true},
		{name: "invalid dimensions", count: 1, modify: func(row []driver.Value) { row[13] = []byte(`{}`) }, wantErr: true},
		{name: "old dimension shape rejected", count: 1, modify: func(row []driver.Value) { row[13] = []byte(`[{"key":"ip","name":"IP"}]`) }, wantErr: true},
		{name: "invalid mapping", count: 1, modify: func(row []driver.Value) { row[12] = []byte(`{}`) }, wantErr: true},
		{name: "oversized JSON projected as NULL", count: 1, modify: func(row []driver.Value) { row[13] = nil }, wantErr: true},
		{name: "padded JSON null", count: 1, modify: func(row []driver.Value) { row[12] = []byte(" null ") }, wantErr: true},
		{name: "JSON null", count: 1, modify: func(row []driver.Value) { row[12] = []byte(`null`) }, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			calls := 0
			db := openReaderTestDB(t, func(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
				calls++
				if tc.queryErr != nil {
					return nil, tc.queryErr
				}
				rows := make([][]driver.Value, tc.count)
				for i := range rows {
					rows[i] = metricTestRow()
					if tc.modify != nil {
						tc.modify(rows[i])
					}
				}
				return &readerTestRows{columns: metricTestColumns(), values: rows}, nil
			})
			client, _ := NewMetricClient(MetricClientConfig{DB: db})
			_, found, err := client.FindMetric(context.Background(), models.MetricQuery{TenantID: "tenant-a", MetricID: 42})
			if found || (err != nil) != tc.wantErr || calls != 1 {
				t.Fatalf("found=%v err=%v calls=%d", found, err, calls)
			}
			if tc.modify != nil && tc.name != "scan failure" && !errors.Is(err, enrich.ErrInvalidDataSourceResponse) {
				t.Fatalf("classification=%v", err)
			}
		})
	}
}

func TestMetricClientLocatorValidation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		query     models.MetricQuery
		rowChange func([]driver.Value)
		wantErr   bool
		wantCalls int
	}{
		{name: "tenant required", query: models.MetricQuery{MetricID: 42}, wantErr: true},
		{name: "negative ID", query: models.MetricQuery{TenantID: "tenant-a", MetricID: -1}, wantErr: true},
		{name: "partial locator", query: models.MetricQuery{TenantID: "tenant-a", FieldName: "usage"}, wantErr: true},
		{name: "model mismatch", query: models.MetricQuery{TenantID: "tenant-a", TableID: "system.cpu", FieldName: "usage", ObjectModelCode: "cw-MySQL"}, wantErr: true, wantCalls: 1},
		{name: "space mismatch", query: models.MetricQuery{TenantID: "tenant-a", TableID: "system.cpu", FieldName: "usage", SpaceUID: "bkcc__2"}, rowChange: func(r []driver.Value) { r[2] = "bkcc__3" }, wantErr: true, wantCalls: 1},
		{name: "locator case mismatch", query: models.MetricQuery{TenantID: "tenant-a", TableID: "System.cpu", FieldName: "usage"}, wantErr: true, wantCalls: 1},
		{name: "native only", query: models.MetricQuery{TenantID: "tenant-a", TableID: "system.cpu", FieldName: "usage"}, rowChange: func(r []driver.Value) { r[8] = "predefined" }, wantErr: true, wantCalls: 1},
		{name: "derived definition", query: models.MetricQuery{TenantID: "tenant-a", FieldName: "derived_usage", FieldTag: models.CWStrategyFieldTagDerivedMetric}, rowChange: func(r []driver.Value) { r[4] = "derived_usage"; r[8] = "derived"; r[9] = nil; r[11] = nil }, wantCalls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			calls := 0
			db := openReaderTestDB(t, func(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
				calls++
				r := metricTestRow()
				if tc.rowChange != nil {
					tc.rowChange(r)
				}
				return &readerTestRows{columns: metricTestColumns(), values: [][]driver.Value{r}}, nil
			})
			client, _ := NewMetricClient(MetricClientConfig{DB: db})
			_, found, err := client.FindMetric(context.Background(), tc.query)
			if (err != nil) != tc.wantErr || found == tc.wantErr || calls != tc.wantCalls {
				t.Fatalf("found=%v err=%v calls=%d", found, err, calls)
			}
		})
	}
}

func TestMetricClientCancellation(t *testing.T) {
	t.Parallel()
	calls := 0
	db := openReaderTestDB(t, func(ctx context.Context, _ string, _ []driver.NamedValue) (driver.Rows, error) {
		calls++
		<-ctx.Done()
		return nil, ctx.Err()
	})
	client, _ := NewMetricClient(MetricClientConfig{DB: db})
	query := models.MetricQuery{TenantID: "tenant-a", MetricID: 42}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := client.FindMetric(ctx, query)
	if !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatalf("canceled err=%v calls=%d", err, calls)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, _, err = client.FindMetric(ctx, query)
	if !errors.Is(err, context.DeadlineExceeded) || calls != 1 {
		t.Fatalf("timeout err=%v calls=%d", err, calls)
	}
	//nolint:staticcheck // 显式验证调用方违反非空 Context 契约时返回错误。
	if _, _, err = client.FindMetric(nil, query); err == nil {
		t.Fatal("nil context accepted")
	}
	if _, err = NewMetricClient(MetricClientConfig{}); err == nil {
		t.Fatal("nil database accepted")
	}
}

func TestMetricClientIDReadsUnboundDefinition(t *testing.T) {
	t.Parallel()
	db := openReaderTestDB(t, func(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
		row := metricTestRow()
		row[3] = nil
		row[8] = "predefined"
		row[9] = nil
		row[10] = nil
		row[11] = nil
		row[12] = []byte(`[]`)
		row[13] = []byte(`[]`)
		return &readerTestRows{columns: metricTestColumns(), values: [][]driver.Value{row}}, nil
	})
	client, _ := NewMetricClient(MetricClientConfig{DB: db})
	got, found, err := client.FindMetric(context.Background(), models.MetricQuery{TenantID: "tenant-a", MetricID: 42})
	if err != nil || !found || got.ObjectModelCode != "" || len(got.Dimensions) != 0 || len(got.ValueMapping) != 0 || got.FieldCNName != "CPU 使用率" {
		t.Fatalf("definition=%#v found=%v err=%v", got, found, err)
	}
}
