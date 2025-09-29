// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lucene_parser_old

import "github.com/spf13/cast"

func getString(fieldExpr Expr) string {
	if fieldExpr != nil {
		if s, ok := fieldExpr.(*StringExpr); ok {
			return s.Value
		}
	}
	return Empty
}

func getValue(expr Expr) string {
	if expr == nil {
		return ""
	}
	switch e := expr.(type) {
	case *StringExpr:
		return e.Value
	case *NumberExpr:
		return cast.ToString(e.Value)
	case *BoolExpr:
		return cast.ToString(e.Value)
	}
	return ""
}
