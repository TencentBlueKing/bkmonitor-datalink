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
	"context"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/doris_parser"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

// TSpider 标识 BKData TSpider 存储。表名侧 Measurement 常为空（无 db.measurement 后缀），
// 用户 SQL 解析与 Doris 共用同一套语法处理。
const TSpider = "tspider"

// TSpiderSQLExpr TSpider 的 SQL 表达式实现，逻辑与 Doris 对齐。
type TSpiderSQLExpr struct {
	DorisSQLExpr
}

var _ SQLExpr = (*TSpiderSQLExpr)(nil)

func (t *TSpiderSQLExpr) Type() string {
	return TSpider
}

func (t *TSpiderSQLExpr) WithInternalFields(timeField, valueField string) SQLExpr {
	t.DorisSQLExpr.WithInternalFields(timeField, valueField)
	return t
}

func (t *TSpiderSQLExpr) WithFieldAlias(fieldAlias metadata.FieldAlias) SQLExpr {
	t.DorisSQLExpr.WithFieldAlias(fieldAlias)
	return t
}

func (t *TSpiderSQLExpr) WithEncode(fn func(string) string) SQLExpr {
	t.DorisSQLExpr.WithEncode(fn)
	return t
}

func (t *TSpiderSQLExpr) WithFieldsMap(fieldsMap metadata.FieldsMap) SQLExpr {
	t.DorisSQLExpr.WithFieldsMap(fieldsMap)
	return t
}

func (t *TSpiderSQLExpr) WithShardKeyTimeBucket(enabled bool) SQLExpr {
	// TSpider 表没有 Doris 的 __shard_key__ 字段，始终走 timeField 时间桶。
	t.DorisSQLExpr.WithShardKeyTimeBucket(false)
	return t
}

func (t *TSpiderSQLExpr) WithKeepColumns(cols []string) SQLExpr {
	t.DorisSQLExpr.WithKeepColumns(cols)
	return t
}

func (t *TSpiderSQLExpr) ParserRangeTime(timeField string, start, end time.Time) string {
	return t.DefaultSQLExpr.ParserRangeTime(timeField, start, end)
}

func (t *TSpiderSQLExpr) ParserSQL(ctx context.Context, q string, tables []string, where string, offset, limit int, tableFieldsMap doris_parser.TableFieldsMap) (string, error) {
	return t.DorisSQLExpr.parserSQL(ctx, q, tables, where, offset, limit, tableFieldsMap, false)
}
