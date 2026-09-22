// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package mysqlstore_test

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	driver "github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	"linkd/internal/activeindex"
	"linkd/internal/domain"
	mysqlstore "linkd/internal/store/mysql"
	"linkd/internal/store/storetest"
)

func TestActiveIndexMySQLIntegration(t *testing.T) {
	dsn := os.Getenv(integrationDSNEnv)
	if dsn == "" {
		t.Skip("LINKD_TEST_MYSQL_DSN is not set")
	}
	cfg, err := driver.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.DBName = ""
	cfg.ParseTime = true
	admin, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = admin.Close() }()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	name := "linkd_index_" + uuid.NewString()[:8]
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE `"+name+"`"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if _, err := admin.ExecContext(cleanup, "DROP DATABASE `"+name+"`"); err != nil {
			t.Error(err)
		}
	}()
	cfg.DBName = name
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	r, err := mysqlstore.New(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ tenant, source, id, label string }{{"tenant", "a", "one", "123"}, {"tenant", "b", "two", "123"}, {"tenant", "a", "three", "00123"}, {"other", "a", "four", "123"}} {
		a := storetest.Alert(test.tenant, test.id, "event", test.id, "warning")
		a.EventSourceID = test.source
		a.Labels["strategy_id"] = domain.NewStringScalar(test.label)
		if test.id == "two" {
			a.Labels["strategy_id"], _ = domain.NewNumberScalar(123)
		}
		if _, err := r.CreateAlert(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	q := activeindex.Query{Sources: []string{"a", "b"}, Scope: &activeindex.Scope{BKTenantID: "tenant", StrategyID: "123"}, MaxRows: 10, MaxBytes: 4096}
	rows, err := r.ReadActiveIndex(ctx, q)
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows=%v error=%v", rows, err)
	}
	q.Scope.StrategyID = "00123"
	rows, err = r.ReadActiveIndex(ctx, q)
	if err != nil || len(rows) != 1 {
		t.Fatalf("exact rows=%v error=%v", rows, err)
	}
	q.Scope = nil
	q.MaxRows = 1
	if rows, err := r.ReadActiveIndex(ctx, q); err == nil || rows != nil {
		t.Fatal("truncated snapshot accepted")
	}
	q.MaxRows = 10
	q.MaxBytes = 1
	if rows, err := r.ReadActiveIndex(ctx, q); err == nil || rows != nil {
		t.Fatal("oversize snapshot accepted")
	}
	q.MaxBytes = 4096
	rows, err = r.ReadActiveIndex(ctx, q)
	if err != nil || len(rows) != 4 {
		t.Fatalf("discovery rows=%v error=%v", rows, err)
	}
}
