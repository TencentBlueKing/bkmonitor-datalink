// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package influxdb

import (
	"context"

	"github.com/influxdata/influxql"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/errno"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

// CheckSelectSQL 检查是否是查询语句，防止sql注入
func CheckSelectSQL(ctx context.Context, sql string) error {
	query, err := influxql.ParseQuery(sql)
	if err != nil {
		codedErr := errno.ErrQueryParseInvalidSQL().
			WithOperation("InfluxQL解析").
			WithError(err).
			WithContexts(map[string]any{
				"SQL": sql,
				"解决":  "检查InfluxQL语法规范，确保字段名和表名正确",
			})

		log.ErrorWithCodef(ctx, codedErr)
		return codedErr
	}
	if len(query.Statements) != 1 {
		codedErr := errno.ErrQueryParseInvalidSQL().
			WithOperation("SQL语句数量检查").
			WithErrorf("语句数量应为1个，实际为%d个", len(query.Statements)).
			WithContexts(map[string]any{
				"SQL": sql,
				"解决":  "确保查询只包含一个语句，移除多余的分号和语句",
			})

		log.ErrorWithCodef(ctx, codedErr)
		return codedErr
	}
	if _, ok := query.Statements[0].(*influxql.SelectStatement); !ok {
		codedErr := errno.ErrQueryParseInvalidSQL().
			WithOperation("SQL类型检查").
			WithErrorf("非SELECT语句，禁止执行").
			WithContexts(map[string]any{
				"SQL": sql,
				"解决":  "只允许执行SELECT查询语句，请使用正确的查询语法",
			})

		log.ErrorWithCodef(ctx, codedErr)
		return codedErr
	}
	return nil
}

// CheckSQLInject 检查sql注入
func CheckSQLInject(sql string) error {
	ctx := context.TODO()
	query, err := influxql.ParseQuery(sql)
	if err != nil {
		codedErr := errno.ErrQueryParseInvalidSQL().
			WithOperation("SQL注入检查-InfluxQL解析").
			WithError(err).
			WithContexts(map[string]any{
				"SQL": sql,
				"解决":  "检查InfluxQL语法规范，确保字段名和表名正确",
			})

		log.ErrorWithCodef(ctx, codedErr)
		return codedErr
	}
	if len(query.Statements) != 1 {
		codedErr := errno.ErrQueryParseInvalidSQL().
			WithOperation("SQL注入检查-语句数量检查").
			WithErrorf("语句数量应为1个，实际为%d个", len(query.Statements)).
			WithContexts(map[string]any{
				"SQL": sql,
				"解决":  "确保查询只包含一个语句，移除多余的分号和语句",
			})

		log.ErrorWithCodef(ctx, codedErr)
		return codedErr
	}
	return nil
}
