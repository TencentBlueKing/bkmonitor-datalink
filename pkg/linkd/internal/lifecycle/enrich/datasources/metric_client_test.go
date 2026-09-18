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
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"linkd/internal/lifecycle/enrich/models"
)

type metricQueryDriver struct {
	queries                  []string
	args                     [][]driver.NamedValue
	modelFound, genericFound bool
	failAt                   int
}

func (d *metricQueryDriver) Open(string) (driver.Conn, error) { return &metricQueryConn{d: d}, nil }

func (d *metricQueryDriver) Connect(context.Context) (driver.Conn, error) { return d.Open("") }

func (d *metricQueryDriver) Driver() driver.Driver { return d }

type metricQueryConn struct{ d *metricQueryDriver }

func (c *metricQueryConn) Prepare(string) (driver.Stmt, error) {
	return nil, fmt.Errorf("unexpected prepare")
}

func (c *metricQueryConn) Close() error { return nil }

func (c *metricQueryConn) Begin() (driver.Tx, error) { return nil, fmt.Errorf("unexpected begin") }

func (c *metricQueryConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.d.queries = append(c.d.queries, q)
	c.d.args = append(c.d.args, append([]driver.NamedValue(nil), args...))
	if len(c.d.queries) == c.d.failAt {
		return nil, errMetricQuery
	}
	model := strings.Contains(q, "object_model_code")
	return &metricQueryRows{found: model && c.d.modelFound || !model && c.d.genericFound}, nil
}

var errMetricQuery = errors.New("injected metric query failure")

type metricQueryRows struct{ found, done bool }

func (r *metricQueryRows) Columns() []string {
	return []string{"field_cn_name", "description", "unit", "dimension_list"}
}

func (r *metricQueryRows) Close() error { return nil }

func (r *metricQueryRows) Next(dest []driver.Value) error {
	if !r.found || r.done {
		return io.EOF
	}
	r.done = true
	dest[0], dest[1], dest[2], dest[3] = "metric name", "description", "percent", []byte("[]")
	return nil
}

func TestMetricModelFallback(t *testing.T) {
	for _, tc := range []struct {
		name, model              string
		modelFound, genericFound bool
		failAt, calls            int
		found                    bool
	}{
		{name: "specific model", model: "host", modelFound: true, genericFound: true, calls: 1, found: true},
		{name: "generic fallback", model: "host", genericFound: true, calls: 2, found: true},
		{name: "without model", genericFound: true, calls: 1, found: true},
		{name: "neither found", model: "host", calls: 2},
		{name: "specific query fails", model: "host", genericFound: true, failAt: 1, calls: 1},
		{name: "fallback query fails", model: "host", failAt: 2, calls: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := &metricQueryDriver{modelFound: tc.modelFound, genericFound: tc.genericFound, failAt: tc.failAt}
			conn := sql.OpenDB(d)
			t.Cleanup(func() {
				if err := conn.Close(); err != nil {
					t.Error(err)
				}
			})
			db, err := gorm.Open(mysql.New(mysql.Config{Conn: conn, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true, Logger: logger.Default.LogMode(logger.Silent)})
			if err != nil {
				t.Fatal(err)
			}
			client, err := NewMetricClient(MetricClientConfig{DB: db})
			if err != nil {
				t.Fatal(err)
			}
			query := models.MetricLibraryQuery{TenantID: "tenant-a", TableID: "system.cpu", FieldName: "usage", FieldTag: "cpu", ObjectModelCode: tc.model}
			value, found, err := client.FindMetricLibrary(t.Context(), query)
			if found != tc.found || errors.Is(err, errMetricQuery) != (tc.failAt > 0) || len(d.queries) != tc.calls {
				t.Fatalf("found=%t err=%v SQL=%v", found, err, d.queries)
			}
			if found && value.FieldCNName != "metric name" {
				t.Fatal("wrong metadata")
			}
			for i, q := range d.queries {
				if !strings.Contains(q, "bk_tenant_id = ?") || !strings.Contains(q, "table_id = ?") || !strings.Contains(q, "tag = ?") || d.args[i][0].Value != "tenant-a" {
					t.Fatal("query lost common tenant or metric filters")
				}
				if i > 0 && strings.Contains(q, "object_model_code") {
					t.Fatal("fallback retained model filter")
				}
			}
			canceled, cancel := context.WithCancel(t.Context())
			cancel()
			if _, _, err := client.FindMetricLibrary(canceled, query); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation lost: %v", err)
			}
			if len(d.queries) != tc.calls {
				t.Fatal("canceled lookup executed SQL")
			}
		})
	}
}
