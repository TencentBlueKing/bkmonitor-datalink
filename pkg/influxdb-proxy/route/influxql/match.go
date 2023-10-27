// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package influxql

import (
	"github.com/influxdata/influxql"
)

// MatchStatementType 匹配sql语句的类型,该方法与MatchSQLType的区别在于，MatchSQLType会有多个statement的情况
func MatchStatementType(sql string) (SQLType, error) {
	stmt, err := influxql.ParseStatement(sql)
	if err != nil {
		return StmtUnknown, err
	}
	var result SQLType

	switch stmt.(type) {
	case *influxql.SelectStatement:
		result = StmtSelect
	case *influxql.ShowDatabasesStatement:
		result = StmtShowDatabases
	case *influxql.ShowMeasurementsStatement:
		result = StmtShowMeasurements
	case *influxql.ShowTagKeysStatement:
		result = StmtShowTagKeys
	case *influxql.ShowTagValuesStatement:
		result = StmtShowTagValues
	case *influxql.ShowFieldKeysStatement:
		result = StmtShowFieldKeys
	case *influxql.ShowSeriesStatement:
		result = StmtShowSeries
	default:
		result = StmtUnknown
		err = ErrUnsupportedStmt
	}
	return result, err
}

// MatchSQLType 匹配sql语句的类型
func MatchSQLType(sql string) ([]SQLType, error) {
	q, err := influxql.ParseQuery(sql)
	if err != nil {
		return []SQLType{StmtUnknown}, err
	}
	stmts := q.Statements

	resultList := make([]SQLType, 0)
	for _, stmt := range stmts {
		var result SQLType
		switch stmt.(type) {
		case *influxql.SelectStatement:
			result = StmtSelect
		case *influxql.ShowDatabasesStatement:
			result = StmtShowDatabases
		case *influxql.ShowMeasurementsStatement:
			result = StmtShowMeasurements
		case *influxql.ShowTagKeysStatement:
			result = StmtShowTagKeys
		case *influxql.ShowTagValuesStatement:
			result = StmtShowTagValues
		case *influxql.ShowFieldKeysStatement:
			result = StmtShowFieldKeys
		case *influxql.ShowSeriesStatement:
			result = StmtShowSeries
		default:
			result = StmtUnknown
			err = ErrUnsupportedStmt
		}
		resultList = append(resultList, result)
	}
	return resultList, err
}
