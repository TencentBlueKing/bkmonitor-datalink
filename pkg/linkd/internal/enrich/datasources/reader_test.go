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
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"testing"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// 在 database/sql 边界提供行，验证真实 GORM SQL、参数、Scan 和取消传播；
// 普通测试不依赖本机数据库，也不全局注册 driver 或增加 mock 依赖。
func openReaderTestDB(t *testing.T, query func(context.Context, string, []driver.NamedValue) (driver.Rows, error)) *gorm.DB {
	t.Helper()
	db := sql.OpenDB(readerTestConnector{query: query})
	db.SetMaxOpenConns(4)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	gormDB, err := gorm.Open(mysql.New(mysql.Config{Conn: db, SkipInitializeWithVersion: true}), &gorm.Config{
		DisableAutomaticPing: true, Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	return gormDB
}

type readerTestConnector struct {
	query func(context.Context, string, []driver.NamedValue) (driver.Rows, error)
}

func (c readerTestConnector) Connect(context.Context) (driver.Conn, error) {
	return readerTestConn(c), nil
}

func (c readerTestConnector) Driver() driver.Driver { return c }

func (c readerTestConnector) Open(string) (driver.Conn, error) {
	return readerTestConn(c), nil
}

type readerTestConn struct {
	query func(context.Context, string, []driver.NamedValue) (driver.Rows, error)
}

func (c readerTestConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}

func (c readerTestConn) Close() error { return nil }

func (c readerTestConn) Begin() (driver.Tx, error) {
	return nil, errors.New("unexpected transaction")
}

func (c readerTestConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	return c.query(ctx, query, args)
}

type readerTestRows struct {
	columns []string
	values  [][]driver.Value
	index   int
	err     error
}

func (r *readerTestRows) Columns() []string { return r.columns }

func (r *readerTestRows) Close() error { return nil }

func (r *readerTestRows) Next(dest []driver.Value) error {
	if r.index == len(r.values) {
		if r.err != nil {
			return r.err
		}
		return io.EOF
	}
	copy(dest, r.values[r.index])
	r.index++
	return nil
}
