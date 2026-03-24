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
	"fmt"

	"github.com/influxdata/influxql"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

// CheckSQLInject 检查sql注入
func CheckSQLInject(ctx context.Context, sql string) error {
	query, err := influxql.ParseQuery(sql)
	if err != nil {
		return metadata.NewMessage(
			metadata.MsgParserSQL,
			"InfluxQL 语法解析",
		).Error(ctx, err)
	}

	if len(query.Statements) != 1 {
		return metadata.NewMessage(
			metadata.MsgParserSQL,
			"InfluxQL 语法解析",
		).Error(ctx, fmt.Errorf("语句数量应为1个，实际为%d个", len(query.Statements)))
	}
	if _, ok := query.Statements[0].(*influxql.SelectStatement); !ok {
		return metadata.NewMessage(
			metadata.MsgParserSQL,
			"InfluxQL 语法解析",
		).Error(ctx, fmt.Errorf("非SELECT语句，禁止执行"))
	}
	return nil
}
