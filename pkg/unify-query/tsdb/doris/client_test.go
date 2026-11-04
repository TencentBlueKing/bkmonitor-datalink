// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package doris

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/bksql"
)

func TestDirectRowsPreserveTypesAndSchema(t *testing.T) {
	db, m, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	c := &Client{db: db}
	tm := time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("CST", 8*3600))
	columns := []*sqlmock.Column{
		sqlmock.NewColumn("id").OfType("BIGINT", int64(0)),
		sqlmock.NewColumn("value").OfType("DECIMAL", ""),
		sqlmock.NewColumn("ts").OfType("DATETIME", tm),
		sqlmock.NewColumn("obj").OfType("JSON", ""),
		sqlmock.NewColumn("missing").OfType("TEXT", "").Nullable(true),
	}
	m.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRowsWithColumnDefinition(columns...).AddRow("9007199254740993", "1.25", tm, `{"a":1}`, nil))
	d, err := c.query(context.Background(), "SELECT id,value,ts,obj,missing FROM t")
	require.NoError(t, err)
	require.Equal(t, 1, d.TotalRecords)
	require.Equal(t, int64(9007199254740993), d.List[0]["id"])
	require.Equal(t, 1.25, d.List[0]["value"])
	require.Equal(t, tm.UnixMilli(), d.List[0]["ts"])
	require.Equal(t, `{"a":1}`, d.List[0]["obj"])
	require.Nil(t, d.List[0]["missing"])
	require.Equal(t, []string{"id", "value", "ts", "obj", "missing"}, d.SelectFieldsOrder)
	require.Equal(t, "long", d.ResultSchema[0]["field_type"])
	m.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRowsWithColumnDefinition(columns...))
	d, err = c.query(context.Background(), "SELECT id FROM t WHERE false")
	require.NoError(t, err)
	require.Empty(t, d.List)
	require.Len(t, d.ResultSchema, 5)
	require.NoError(t, m.ExpectationsWereMet())
}

func TestDirectRawReusesBKSQL(t *testing.T) {
	mock.Init()
	db, m, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	target := metadata.PhysicalTable{Database: "logs", Table: "history", AnalyzedFields: []string{"log"}}
	c := &Client{db: db, tables: map[string]metadata.PhysicalTable{metadata.PhysicalTableSQL(target): target}}
	m.ExpectExec("SET describe_extend_variant_column = true").WillReturnResult(sqlmock.NewResult(0, 0))
	m.ExpectQuery("DESC `logs`.`history`").WillReturnRows(sqlmock.NewRows([]string{"Field", "Type"}).AddRow("dtEventTimeStamp", "BIGINT").AddRow("thedate", "INT").AddRow("dtEventTime", "VARCHAR(32)").AddRow("log", "TEXT"))
	m.ExpectQuery("SELECT .* FROM `logs`.`history` WHERE .* LIMIT 10").WillReturnRows(sqlmock.NewRowsWithColumnDefinition(sqlmock.NewColumn("log").OfType("TEXT", "")).AddRow("hello"))
	ctx := metadata.InitHashID(context.Background())
	i, err := bksql.NewInstance(ctx, &bksql.Options{Client: c, Timeout: time.Second, MaxLimit: 10})
	require.NoError(t, err)
	q := &metadata.Query{StorageType: metadata.BkSqlStorageType, Measurement: metadata.DorisStorageType, QueryBackend: metadata.DorisStorageType, DB: "1001_logical", TableID: "logical", DirectTables: []metadata.PhysicalTable{target}, Size: 10}
	ch := make(chan map[string]any, 1)
	size, total, _, err := i.QueryRawData(ctx, q, time.Unix(1700000000, 0), time.Unix(1700000060, 0), ch)
	require.NoError(t, err)
	require.EqualValues(t, 1, size)
	require.EqualValues(t, 1, total)
	require.Equal(t, "hello", (<-ch)["log"])
	require.NoError(t, m.ExpectationsWereMet())
}

func TestDirectQueryFailureIsNotEmptySuccess(t *testing.T) {
	for _, failure := range []error{errors.New("permission denied"), context.DeadlineExceeded} {
		db, m, err := sqlmock.New()
		require.NoError(t, err)
		m.ExpectQuery("SELECT").WillReturnError(failure)
		res := (&Client{db: db}).QuerySync(context.Background(), bksql.QuerySyncRequest{SQL: "SELECT * FROM t"}, nil)
		require.False(t, res.Result)
		require.ErrorIs(t, res.Err, failure)
		require.NoError(t, m.ExpectationsWereMet())
		db.Close()
	}
	db, m, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	m.ExpectExec("SET").WillReturnError(sql.ErrConnDone)
	_, err = (&Client{db: db}).query(context.Background(), "DESC t")
	require.ErrorIs(t, err, sql.ErrConnDone)
	require.NoError(t, m.ExpectationsWereMet())
}

func TestDirectQueryCancellation(t *testing.T) {
	db, m, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	m.ExpectQuery("SELECT").WillDelayFor(time.Second).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(1))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	res := (&Client{db: db}).QuerySync(ctx, bksql.QuerySyncRequest{SQL: "SELECT 1"}, nil)
	require.False(t, res.Result)
	require.Error(t, res.Err)
	require.Nil(t, res.Data)
	require.NoError(t, m.ExpectationsWereMet())
}
