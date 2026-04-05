// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package sql_expr

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
