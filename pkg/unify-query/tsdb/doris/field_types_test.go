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
	"database/sql/driver"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

// Exercise the full row scan and schema mapping, including a SQL NULL of each type.
func TestFieldTypeWireContract(t *testing.T) {
	stamp := time.Date(2026, 11, 20, 0, 0, 0, 123456000, time.FixedZone("UTC+8", 8*3600))
	for _, tc := range []struct {
		name, wire, schema string
		input              driver.Value
		want               any
	}{
		{"bool false", "BOOLEAN", "int", false, int64(0)},
		{"bool true", "BOOLEAN", "int", true, int64(1)},
		{"tinyint min", "TINYINT", "int", []byte("-128"), int64(-128)},
		{"smallint max", "SMALLINT", "int", "32767", int64(32767)},
		{"int min", "INT", "int", "-2147483648", int64(-2147483648)},
		{"bigint beyond JS mantissa", "BIGINT", "long", "9007199254740993", int64(9007199254740993)},
		{"bigint min", "BIGINT", "long", "-9223372036854775808", int64(-9223372036854775807 - 1)},
		{"unsigned bigint max", "UNSIGNED BIGINT", "long", "18446744073709551615", uint64(18446744073709551615)},
		{"float exponent", "FLOAT", "float", []byte("-1.25e2"), float64(-125)},
		{"double fraction", "DOUBLE", "double", "0.125", float64(0.125)},
		{"decimal scale", "NEWDECIMAL", "double", "-12.5000", float64(-12.5)},
		{"decimal float64 rounding", "DECIMAL", "double", "9007199254740993.00", float64(9007199254740992)},
		{"date local midnight", "DATE", "string", stamp, "2026-11-20"},
		{"datetime milliseconds", "DATETIME", "long", stamp, int64(1795104000123)},
		{"timestamp milliseconds", "TIMESTAMP", "long", stamp, int64(1795104000123)},
		{"empty text", "VARCHAR", "string", []byte(""), ""},
		{"char whitespace", "CHAR", "string", " x ", " x "},
		{"unicode text", "TEXT", "string", []byte("中文😀"), "中文😀"},
		{"JSON object", "JSON", "string", []byte(`{"a":[1,null]}`), `{"a":[1,null]}`},
		{"variant literal null", "VARIANT", "string", "null", "null"},
		{"array text", "ARRAY", "string", "[1,null,2]", "[1,null,2]"},
		{"map text", "MAP", "string", `{"k":1}`, `{"k":1}`},
		{"struct text", "STRUCT", "string", `{"k":"v"}`, `{"k":"v"}`},
		{"largeint text", "LARGEINT", "string", "170141183460469231731687303715884105727", "170141183460469231731687303715884105727"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			col := sqlmock.NewColumn("v").OfType(tc.wire, tc.input).Nullable(true)
			mock.ExpectQuery("SELECT v").WillReturnRows(sqlmock.NewRowsWithColumnDefinition(col).AddRow(tc.input).AddRow(nil))
			got, err := (&Client{db: db}).query(context.Background(), "SELECT v FROM sample")
			require.NoError(t, err)
			require.Len(t, got.List, 2)
			require.Equal(t, tc.want, got.List[0]["v"])
			require.Contains(t, got.List[1], "v")
			require.Nil(t, got.List[1]["v"])
			require.Equal(t, tc.schema, got.ResultSchema[0]["field_type"])
		})
	}
}
