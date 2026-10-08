// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metadatastore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	driver "github.com/go-sql-driver/mysql"
	"linkd/internal/onemodel"
)

func TestAttributeDirectoryValidation(t *testing.T) {
	types, err := attributeTypes([]byte(`{"config":[{"bk_property_id":"cpu","bk_property_type":"int"},{"bk_property_id":"tag","bk_property_type":"string"}]}`))
	if err != nil || types["cpu"] != onemodel.InstanceAttributeLong || types["tag"] != onemodel.InstanceAttributeKeyword {
		t.Fatalf("types %+v %v", types, err)
	}
	for _, raw := range []string{`{"config":[{"bk_property_id":"cpu"},{"bk_property_id":"cpu"}]}`, `{"config":[{"bk_property_id":""}]}`, `{"config":[],"config":[]}`, `{`} {
		if _, err := attributeTypes([]byte(raw)); err == nil {
			t.Fatal("invalid attribute directory accepted")
		}
	}
	if _, err := New(nil); err == nil {
		t.Fatal("nil database accepted")
	}
}

// TestMySQLTargetDirectory 仅读写测试新建数据库，用真实 MySQL 校验租户条件和实时定义更新。
func TestMySQLTargetDirectory(t *testing.T) {
	dsn := os.Getenv("LINKD_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set LINKD_TEST_MYSQL_DSN")
	}
	parsed, err := driver.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	parsed.DBName = ""
	admin, err := sql.Open("mysql", parsed.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = admin.Close() }()
	database := fmt.Sprintf("linkd_target_%d_%d", os.Getpid(), time.Now().UnixNano())
	if _, err := admin.ExecContext(t.Context(), "CREATE DATABASE `"+database+"`"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(ctx, "DROP DATABASE `"+database+"`"); err != nil {
			t.Error(err)
		}
	}()
	parsed.DBName = database
	db, err := sql.Open("mysql", parsed.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	for _, ddl := range []string{
		`CREATE TABLE metadata_space (bk_tenant_id VARCHAR(64),space_type_id VARCHAR(16),space_id VARCHAR(64),is_global BOOLEAN)`,
		`CREATE TABLE object_model_v2 (bk_tenant_id VARCHAR(64),model_id VARCHAR(128),datasource VARCHAR(32),bk_cmdb_obj_id VARCHAR(255),attribute_config JSON)`,
		`CREATE TABLE dynamic_group_v2 (bk_tenant_id VARCHAR(64),dynamic_group_id BIGINT,object_model_code VARCHAR(128),space_code VARCHAR(128),condition_list JSON)`,
	} {
		if _, err := db.ExecContext(t.Context(), ddl); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(t.Context(), "INSERT INTO metadata_space VALUES(?,?,?,?),(?,?,?,?)", "t", "bkcc", "99", true, "t", "bkcc", "2", false); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), "INSERT INTO object_model_v2 VALUES(?,?,?,?,?)", "t", "cw-Host", "cmdb", "host", `{"config":[{"bk_property_id":"cpu","bk_property_type":"int"}]}`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), "INSERT INTO dynamic_group_v2 VALUES(?,?,?,?,?)", "t", 23, "cw-Host", "bkcc__2", `[{"field":"cpu","value":"2"}]`); err != nil {
		t.Fatal(err)
	}
	directory, _ := New(db)
	space, found, err := directory.Space(t.Context(), "t", 99)
	if err != nil || !found || !space.Global {
		t.Fatalf("space %+v %v", space, err)
	}
	model, found, err := directory.Model(t.Context(), "t", "cw-Host")
	if err != nil || !found || model.AttributeTypes["cpu"] != onemodel.InstanceAttributeLong {
		t.Fatalf("model %+v %v", model, err)
	}
	group, found, err := directory.DynamicGroup(t.Context(), "t", "23")
	if err != nil || !found || group.SpaceCode != "bkcc__2" {
		t.Fatalf("group %+v %v", group, err)
	}
	if _, err := db.ExecContext(t.Context(), "UPDATE dynamic_group_v2 SET condition_list=? WHERE bk_tenant_id=? AND dynamic_group_id=?", `[{"field":"cpu","value":"5"}]`, "t", 23); err != nil {
		t.Fatal(err)
	}
	newer, found, err := directory.DynamicGroup(t.Context(), "t", "23")
	if err != nil || !found || string(newer.Conditions) == string(group.Conditions) {
		t.Fatal("group definition cached")
	}
	if _, found, err := directory.Model(t.Context(), "other", "cw-Host"); err != nil || found {
		t.Fatal("model tenant leaked")
	}
	if _, found, err := directory.Space(t.Context(), "other", 99); err != nil || found {
		t.Fatal("space tenant leaked")
	}
	if _, found, err := directory.DynamicGroup(t.Context(), "other", "23"); err != nil || found {
		t.Fatal("group tenant leaked")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := directory.DynamicGroup(ctx, "t", "23"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	// 不依赖表唯一约束兜底；重复身份必须明确失败，不能任意挑一行。
	if _, err := db.ExecContext(t.Context(), "INSERT INTO metadata_space VALUES(?,?,?,?)", "t", "bkcc", "99", false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := directory.Space(t.Context(), "t", 99); !errors.Is(err, onemodel.ErrInvalidDataSourceResponse) {
		t.Fatalf("duplicate accepted %v", err)
	}
	models, err := directory.CMDBModels(t.Context(), "t")
	if err != nil || len(models) != 1 || models[0].CMDBObjectID != "host" {
		t.Fatal("CMDB node mapping missing", err)
	}
	if foreign, err := directory.CMDBModels(t.Context(), "other"); err != nil || len(foreign) != 0 {
		t.Fatal("CMDB mapping crossed tenant", err)
	}
	if _, err := directory.CMDBModels(ctx, "t"); !errors.Is(err, context.Canceled) {
		t.Fatal("CMDB mapping ignored cancellation", err)
	}
	if _, err := db.ExecContext(t.Context(), "INSERT INTO object_model_v2 VALUES(?,?,?,?,?)", "t", "duplicate-host", "cmdb", "host", `{"config":[]}`); err != nil {
		t.Fatal(err)
	}
	if _, err := directory.CMDBModels(t.Context(), "t"); !errors.Is(err, onemodel.ErrInvalidDataSourceResponse) {
		t.Fatal("ambiguous model mapping accepted", err)
	}
}
