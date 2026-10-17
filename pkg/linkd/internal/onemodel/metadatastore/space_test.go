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
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

type spaceQuery func(context.Context, string, []driver.NamedValue) (driver.Rows, error)

type spaceConnector struct{ query spaceQuery }

func (c spaceConnector) Connect(context.Context) (driver.Conn, error) { return spaceConnection(c), nil }

func (c spaceConnector) Driver() driver.Driver { return spaceDriver(c) }

type spaceDriver spaceConnector

func (d spaceDriver) Open(string) (driver.Conn, error) { return spaceConnection(d), nil }

type spaceConnection spaceConnector

func (c spaceConnection) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}

func (c spaceConnection) Begin() (driver.Tx, error) { return nil, errors.New("unexpected transaction") }

func (c spaceConnection) Close() error { return nil }

func (c spaceConnection) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	return c.query(ctx, q, args)
}

type spaceRows struct{ values []driver.Value }

func (r *spaceRows) Columns() []string {
	return []string{"bk_tenant_id", "space_id", "is_global", "id"}
}

func (r *spaceRows) Close() error { return nil }

func (r *spaceRows) Next(out []driver.Value) error {
	if r.values == nil {
		return io.EOF
	}
	copy(out, r.values)
	r.values = nil
	return nil
}

func TestSpaceDirectoryResolvesNonBKCCPrimaryKey(t *testing.T) {
	for _, test := range []struct {
		code, kind, id string
		pk, want       int64
		global         bool
	}{
		{"default__custom", "default", "custom", 42, -42, false},
		{"bcs__cluster-a", "bcs", "cluster-a", 43, -43, false},
		{"bkcc__99", "bkcc", "99", 44, 99, true},
	} {
		t.Run(test.code, func(t *testing.T) {
			db := sql.OpenDB(spaceConnector{query: func(_ context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
				if !strings.Contains(q, "WHERE bk_tenant_id=? AND space_type_id=? AND space_id=?") {
					t.Fatal(q)
				}
				got := []any{args[0].Value, args[1].Value, args[2].Value}
				if !reflect.DeepEqual(got, []any{"tenant-a", test.kind, test.id}) {
					return &spaceRows{}, nil
				}
				return &spaceRows{values: []driver.Value{"tenant-a", test.id, true, test.pk}}, nil
			}})
			defer func() { _ = db.Close() }()
			directory, _ := New(db)
			scope, found, err := directory.Space(t.Context(), "tenant-a", test.code)
			if err != nil || !found || scope.BusinessID != test.want || scope.Global != test.global {
				t.Fatalf("scope %+v found %v err %v", scope, found, err)
			}
			if _, found, err := directory.Space(t.Context(), "tenant-b", test.code); err != nil || found {
				t.Fatal("cross-tenant lookup", found, err)
			}
		})
	}
}
