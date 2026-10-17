// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package assembly

import (
	"context"
	"database/sql"
	"os"
	"sync"
	"testing"
	"time"

	driver "github.com/go-sql-driver/mysql"
)

// TestMySQLPoolRetainsBurstConnections 使用真实 SQL Connection_ID 验证连接复用，
// 只打开本用例的新连接池，不创建表、不修改数据或服务器设置。
func TestMySQLPoolRetainsBurstConnections(t *testing.T) {
	dsn := os.Getenv("LINKD_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set LINKD_TEST_MYSQL_DSN")
	}
	cfg, err := driver.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	// 本测试仅执行连接级只读 SQL，不依赖传入数据库中的表。
	cfg.DBName = ""
	cfg.Timeout = 3 * time.Second
	for _, tc := range []struct {
		name             string
		open, concurrent int
		legacy           bool
	}{
		{"legacy_bounded", 4, 4, true},
		{"bounded", 4, 4, false},
		{"legacy_unlimited", 0, 8, true},
		{"unlimited", 0, 8, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := sql.Open("mysql", cfg.FormatDSN())
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := db.Close(); err != nil {
					t.Error(err)
				}
			}()
			if tc.legacy {
				db.SetMaxOpenConns(tc.open)
				db.SetMaxIdleConns(max(tc.open/2, 2))
				db.SetConnMaxLifetime(databaseMaxLifetime)
			} else {
				configureMySQLPool(db, tc.open)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			allIDs := map[int64]bool{}
			for wave := 0; wave < 6; wave++ {
				// 每波同时租借全部连接并查询服务器连接身份，放回后下一波再取。
				// 不用 Sleep/事务/锁模拟慢查询，不影响其他测试连接。
				connections := make([]*sql.Conn, tc.concurrent)
				for i := range connections {
					connections[i], err = db.Conn(ctx)
					if err != nil {
						t.Fatal(err)
					}
					conn := connections[i]
					t.Cleanup(func() { _ = conn.Close() })
				}
				ids := make([]int64, len(connections))
				errs := make([]error, len(connections))
				var wg sync.WaitGroup
				for i, conn := range connections {
					wg.Go(func() { errs[i] = conn.QueryRowContext(ctx, "SELECT CONNECTION_ID()").Scan(&ids[i]) })
				}
				wg.Wait()
				for i, conn := range connections {
					closeErr := conn.Close()
					if errs[i] != nil || closeErr != nil {
						t.Fatal(errs[i], closeErr)
					}
					allIDs[ids[i]] = true
				}
			}
			stats := db.Stats()
			t.Logf("waves=6 concurrent=%d unique_server_connections=%d idle=%d open=%d max_idle_closed=%d lifetime_closed=%d wait_count=%d", tc.concurrent, len(allIDs), stats.Idle, stats.OpenConnections, stats.MaxIdleClosed, stats.MaxLifetimeClosed, stats.WaitCount)
			if tc.legacy {
				if len(allIDs) <= tc.concurrent || stats.MaxIdleClosed == 0 {
					t.Fatal("legacy pool did not reproduce repeated handshakes")
				}
			} else if len(allIDs) != tc.concurrent || stats.MaxIdleClosed != 0 || stats.Idle != tc.concurrent || stats.MaxLifetimeClosed != 0 {
				t.Fatalf("burst reuse failed: ids=%d stats=%+v", len(allIDs), stats)
			}
		})
	}
}
