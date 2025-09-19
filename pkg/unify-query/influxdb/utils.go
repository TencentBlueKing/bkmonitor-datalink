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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/errors"
)

// CheckSelectSQL 检查是否是查询语句，防止sql注入
func CheckSelectSQL(ctx context.Context, sql string) error {
	query, err := influxql.ParseQuery(sql)
	if err != nil {
		log.Errorf(ctx, "%s [%s] | SQL: %s | 错误: %s | 解决: 检查InfluxQL语法规范", errors.ErrQueryParseInvalidSQL, errors.GetErrorCode(errors.ErrQueryParseInvalidSQL), sql, err)
		return err
	}
	if len(query.Statements) != 1 {
		log.Errorf(ctx, "%s [%s] | SQL: %s | 问题: 语句数量应为1个 | 解决: 确保查询只包含一个语句", errors.ErrQueryParseUnsupported, errors.GetErrorCode(errors.ErrQueryParseUnsupported), sql)
		return ErrWrongInfluxdbSQLFormat
	}
	if _, ok := query.Statements[0].(*influxql.SelectStatement); !ok {
		log.Errorf(ctx, "%s [%s] | SQL: %s | 问题: 非SELECT语句 | 解决: 使用SELECT查询语句", errors.ErrQueryParseUnsupported, errors.GetErrorCode(errors.ErrQueryParseUnsupported), sql)
		return ErrWrongInfluxdbSQLFormat
	}
	return nil
}

// CheckSQLInject 检查sql注入
func CheckSQLInject(sql string) error {
	query, err := influxql.ParseQuery(sql)
	if err != nil {
		log.Errorf(context.TODO(), "%s [%s] | SQL: %s | 错误: %s | 解决: 检查InfluxQL语法规范", errors.ErrQueryParseInvalidSQL, errors.GetErrorCode(errors.ErrQueryParseInvalidSQL), sql, err)
		return err
	}
	if len(query.Statements) != 1 {
		log.Errorf(context.TODO(), "%s [%s] | SQL: %s | 问题: 语句数量应为1个 | 解决: 确保查询只包含一个语句", errors.ErrQueryParseUnsupported, errors.GetErrorCode(errors.ErrQueryParseUnsupported), sql)
		return ErrWrongInfluxdbSQLFormat
	}
	return nil
}
