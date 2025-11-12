// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package query

import (
	"sort"

	"github.com/spf13/cast"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/function"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

func SortSliceListWithTime(list []map[string]any, os metadata.Orders, fieldType map[string]string) {
	if len(os) == 0 {
		return
	}
	if len(list) == 0 {
		return
	}

	sort.SliceStable(list, func(i, j int) bool {
		for _, o := range os {
			a := list[i][o.Name]
			b := list[j][o.Name]

			// 添加nil安全检查，考虑o.Ast排序方向
			if a == nil && b == nil {
				continue
			}
			if a == nil {
				// 升序时nil排在后面(false)，降序时nil排在前面(true)
				return !o.Ast
			}
			if b == nil {
				// 升序时nil排在后面(true)，降序时nil排在前面(false)
				return o.Ast
			}

			if a == b {
				continue
			}

			if ft, ok := fieldType[o.Name]; ok {
				switch ft {
				case metadata.TypeDate, metadata.TypeDateNanos:
					t1 := function.StringToNanoUnix(cast.ToString(a))
					t2 := function.StringToNanoUnix(cast.ToString(b))

					if t1 > 0 && t2 > 0 {
						if o.Ast {
							return t1 < t2
						} else {
							return t1 > t2
						}
					}
				}
			}

			// 如果是 float 格式则使用 float 进行对比
			f1, f1Err := cast.ToFloat64E(a)
			f2, f2Err := cast.ToFloat64E(b)
			if f1Err == nil && f2Err == nil {
				if o.Ast {
					return f1 < f2
				} else {
					return f1 > f2
				}
			}

			// 最后使用 string 的方式进行排序
			t1 := cast.ToString(a)
			t2 := cast.ToString(b)
			if o.Ast {
				return t1 < t2
			} else {
				return t1 > t2
			}
		}
		return false
	})
}
