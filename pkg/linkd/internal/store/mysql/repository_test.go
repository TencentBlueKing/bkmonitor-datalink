// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
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
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	driver "github.com/go-sql-driver/mysql"
	"linkd/internal/store"
	mysqlstore "linkd/internal/store/mysql"
	"linkd/internal/store/storetest"
)

const integrationDSNEnv = "LINKD_TEST_MYSQL_DSN"

func TestNewRejectsNilDB(t *testing.T) {
	t.Parallel()
	if _, err := mysqlstore.New(nil); err == nil {
		t.Fatal("New(nil) unexpectedly succeeded")
	}
}

func TestRepositoryContract(t *testing.T) {
	dsn := os.Getenv(integrationDSNEnv)
	if dsn == "" {
		t.Skipf("set %s to run the MySQL integration contract", integrationDSNEnv)
	}
	baseConfig, err := driver.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("parse %s: %v", integrationDSNEnv, err)
	}
	adminConfig := baseConfig.Clone()
	adminConfig.DBName = ""
	adminDB, err := sql.Open("mysql", adminConfig.FormatDSN())
	if err != nil {
		t.Fatalf("open mysql admin connection: %v", err)
	}
	t.Cleanup(func() { _ = adminDB.Close() })
	pingContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := adminDB.PingContext(pingContext); err != nil {
		t.Fatalf("ping mysql: %v", err)
	}

	var sequence atomic.Uint64
	storetest.RunRepositoryContract(t, func(t *testing.T) store.Repository {
		t.Helper()
		databaseName := "linkd_contract_" + strconv.Itoa(os.Getpid()) + "_" +
			strconv.FormatUint(sequence.Add(1), 10)
		if _, err := adminDB.ExecContext(
			context.Background(),
			"CREATE DATABASE `"+databaseName+"` CHARACTER SET utf8mb4 COLLATE utf8mb4_bin",
		); err != nil {
			t.Fatalf("create test database %s: %v", databaseName, err)
		}
		t.Cleanup(func() {
			cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cleanupCancel()
			if _, err := adminDB.ExecContext(
				cleanupContext,
				"DROP DATABASE IF EXISTS `"+databaseName+"`",
			); err != nil {
				t.Errorf("drop test database %s: %v", databaseName, err)
			}
		})

		testConfig := baseConfig.Clone()
		testConfig.DBName = databaseName
		db, err := sql.Open("mysql", testConfig.FormatDSN())
		if err != nil {
			t.Fatalf("open test database %s: %v", databaseName, err)
		}
		t.Cleanup(func() {
			if err := db.Close(); err != nil {
				t.Errorf("close test database %s: %v", databaseName, err)
			}
		})
		repository, err := mysqlstore.New(db)
		if err != nil {
			t.Fatalf("new mysql repository: %v", err)
		}
		if err := repository.EnsureSchema(context.Background()); err != nil {
			t.Fatalf("ensure mysql schema: %v", err)
		}
		if err := repository.EnsureSchema(context.Background()); err != nil {
			t.Fatalf("ensure mysql schema idempotently: %v", err)
		}
		return repository
	})
}
