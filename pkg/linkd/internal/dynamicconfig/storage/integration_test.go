// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package storage

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	driver "github.com/go-sql-driver/mysql"
	"linkd/internal/config"
	"linkd/internal/dynamicconfig"
)

func TestSnapshotStoreIntegration(t *testing.T) {
	for _, backend := range []string{"mysql", "elasticsearch"} {
		t.Run(backend, func(t *testing.T) {
			cfg := config.StorageConfig{Repository: backend}
			key := "dynamic-test-" + strconv.FormatInt(time.Now().UnixNano(), 10)
			if backend == "mysql" {
				dsn := os.Getenv("LINKD_TEST_MYSQL_DSN")
				if dsn == "" {
					t.Skip("set LINKD_TEST_MYSQL_DSN to run MySQL snapshot integration")
				}
				d, err := driver.ParseDSN(dsn)
				if err != nil {
					t.Fatal("invalid test MySQL DSN")
				}
				cfg.MySQL = &config.MySQLConfig{Address: d.Addr, Database: d.DBName, Username: d.User, Password: d.Passwd}
			} else {
				url := os.Getenv("LINKD_TEST_ELASTICSEARCH_URL")
				if url == "" {
					t.Skip("set LINKD_TEST_ELASTICSEARCH_URL to run Elasticsearch snapshot integration")
				}
				cfg.Elasticsearch = &config.ElasticsearchConfig{Addresses: []string{url}, IndexPrefix: key, APIKey: os.Getenv("LINKD_TEST_ELASTICSEARCH_API_KEY")}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			s, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = s.Close() }()
			defer func() {
				cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
				defer stop()
				if s.db != nil {
					_, _ = s.db.ExecContext(cleanup, "DELETE FROM "+table+" WHERE id=?", key)
				} else {
					_, _, _ = s.request(cleanup, http.MethodDelete, "/"+s.index, nil)
				}
			}()
			r := testRecord()
			if err = s.Save(ctx, key, "", r); err != nil {
				t.Fatal(err)
			}
			restarted, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = restarted.Close() }()
			saved, token, err := restarted.Load(ctx, key)
			if err != nil || saved.Snapshot.Digest != r.Snapshot.Digest {
				t.Fatalf("restore failed: %v", err)
			}
			if err = restarted.Save(ctx, key, token, r); err != nil {
				t.Fatal(err)
			}
			if err = s.Save(ctx, key, token, r); !errors.Is(err, dynamicconfig.ErrConflict) {
				t.Fatalf("stale CAS not rejected: %v", err)
			}
		})
	}
}
