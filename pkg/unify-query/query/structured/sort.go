// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package structured

import (
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

type OrderBy []string

// OrderBy 转换为 metadata orders 格式
func (ob OrderBy) Orders() metadata.Orders {
	orders := make(metadata.Orders, 0, len(ob))
	for _, o := range ob {
		if len(o) == 0 {
			continue
		}

		asc := true
		name := o

		if strings.HasPrefix(o, "-") {
			asc = false
			name = name[1:]
		}
		orders = append(orders, metadata.Order{
			Name: name,
			Ast:  asc,
		})
	}
	return orders
}
