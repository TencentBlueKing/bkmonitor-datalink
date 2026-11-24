// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package onemodel

import (
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

// TestDorisOneModelContract 只读真实 KAC Doris 实例表；没有显式环境时跳过，不连接开发者默认数据库。
func TestDorisOneModelContract(t *testing.T) {
	dsn := os.Getenv("LINKD_TEST_DORIS_DSN")
	tenant := os.Getenv("LINKD_TEST_ONEMODEL_TENANT_ID")
	model := os.Getenv("LINKD_TEST_ONEMODEL_MODEL_ID")
	id := os.Getenv("LINKD_TEST_ONEMODEL_INSTANCE_ID")
	if dsn == "" || tenant == "" || model == "" || id == "" {
		t.Skip("set LINKD_TEST_DORIS_DSN and LINKD_TEST_ONEMODEL_* for real Doris contract")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal("invalid Doris test connection")
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(2)
	c, err := NewDorisClient(DorisConfig{Reader: db, Timeout: 5 * time.Second, InstanceTable: os.Getenv("LINKD_TEST_DORIS_INSTANCE_TABLE"), EdgeTable: os.Getenv("LINKD_TEST_DORIS_EDGE_TABLE")})
	if err != nil {
		t.Fatal(err)
	}
	item, found, err := c.FindInstance(t.Context(), tenant, InstanceQuery{ModelCode: model, InstanceID: id})
	if err != nil || !found || item.InstanceID != id {
		t.Fatal("Doris identity query failed", err)
	}
	if _, found, err = c.FindInstance(t.Context(), tenant+"-linkd-missing", InstanceQuery{ModelCode: model, InstanceID: id}); err != nil || found {
		t.Fatal("Doris tenant isolation failed", err)
	}
	p, err := NewPager(c)
	if err != nil {
		t.Fatal(err)
	}
	page, err := p.Search(t.Context(), tenant, PageQuery{ModelID: model, Limit: 1})
	if err != nil || len(page.Instances) != 1 {
		t.Fatal("Doris pagination failed", err)
	}
	if page.NextCursor != "" {
		next, err := p.Search(t.Context(), tenant, PageQuery{ModelID: model, Limit: 1, Cursor: page.NextCursor})
		if err != nil || len(next.Instances) != 1 || next.Instances[0].InstanceID <= page.Instances[0].InstanceID {
			t.Fatal("Doris keyset pagination failed", err)
		}
	}
}
