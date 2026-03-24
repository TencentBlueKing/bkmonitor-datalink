// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package resulttable

import (
	"strings"
)

// TableIdsLike filter many table id by `like`
// table_id LIKE ? OR table_id LIKE ?", "L12%", "A12%
func (qs ResultTableQuerySet) TableIdsLike(tableIds []string) ResultTableQuerySet {
	var sqlList []string
	interfaceSlice := make([]any, len(tableIds))
	for i, v := range tableIds {
		sqlList = append(sqlList, "table_id LIKE ?")
		interfaceSlice[i] = v
	}
	// 以 `OR` 拼接 sql
	sql := strings.Join(sqlList, " OR ")
	return qs.w(qs.db.Where(sql, interfaceSlice...))
}
