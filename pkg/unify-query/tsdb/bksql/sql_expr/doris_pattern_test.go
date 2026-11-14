// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package sql_expr

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDorisSearchAfterNumberLiteral(t *testing.T) {
	d := NewSQLExpr(Doris).(*DorisSQLExpr)
	for _, number := range []string{"1", "-1", "+1.5", "1.", ".5", "1e10", "-2.5E-3"} {
		got, err := d.searchAfterLiteral(number, DorisTypeBigInt)
		require.NoError(t, err, number)
		assert.Equal(t, number, got)
	}
	for _, number := range []string{"", "abc", "1.2.3", "1e", "e5", "0x10", " 1", "1 ", "."} {
		_, err := d.searchAfterLiteral(number, DorisTypeBigInt)
		assert.ErrorContains(t, err, "is not a valid number", number)
	}
}

func TestDorisCaseAsArray(t *testing.T) {
	d := NewSQLExpr(Doris).(*DorisSQLExpr)
	for _, c := range []struct {
		in, want string
		ok       bool
	}{
		{"ARRAY<INT>", "INT ARRAY", true},
		{"ARRAY<STRING>", "STRING ARRAY", true},
		{"MAP<ARRAY<INT>>", "INT ARRAY", true},
		{"ARRAY<>", "ARRAY<>", false},
		{"STRING", "STRING", false},
		{"", DorisTypeString, false},
	} {
		got, ok := d.caseAs(c.in)
		assert.Equal(t, c.want, got, c.in)
		assert.Equal(t, c.ok, ok, c.in)
	}
}

func TestDorisGetFieldMinute(t *testing.T) {
	d := NewSQLExpr(Doris).(*DorisSQLExpr)
	for _, c := range []struct {
		in string
		ok bool
	}{
		{"minute5", true},
		{"MINUTE60", true},
		{"xminute1y", true},
		{"minute", false},
		{"hour1", false},
	} {
		_, ok := d.getField(c.in)
		assert.Equal(t, c.ok, ok, c.in)
	}
}
