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
)

// CheckSelectSQL 检查是否是查询语句，防止sql注入
func CheckSelectSQL(ctx context.Context, sql string) error {
	query, err := influxql.ParseQuery(sql)
	if err != nil {
		log.Errorf(ctx, "parse query:%s failed,error:%s", sql, err)
		return err
	}
	if len(query.Statements) != 1 {
		log.Errorf(ctx, "get wrong format query:%s,statement should only one", sql)
		return ErrWrongInfluxdbSQLFormat
	}
	if _, ok := query.Statements[0].(*influxql.SelectStatement); !ok {
		log.Errorf(ctx, "get wrong format query:%s which is not a select statement", sql)
		return ErrWrongInfluxdbSQLFormat
	}
	return nil
}

// CheckSQLInject 检查sql注入
func CheckSQLInject(sql string) error {
	query, err := influxql.ParseQuery(sql)
	if err != nil {
		log.Errorf(context.TODO(), "parse query:%s failed,error:%s", sql, err)
		return err
	}
	if len(query.Statements) != 1 {
		log.Errorf(context.TODO(), "get wrong format query:%s,statement should only one", sql)
		return ErrWrongInfluxdbSQLFormat
	}
	return nil
}
