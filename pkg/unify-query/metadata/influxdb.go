// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metadata

import (
	"fmt"

	"github.com/influxdata/influxql"
)

// Tag represents a single key/value tag pair.
type Tag struct {
	Key   []byte
	Value []byte
}

type Tags []Tag

func ParseCondition(condition string) (Tags, error) {
	expr, err := influxql.ParseExpr(condition)
	if err != nil {
		return nil, err
	}
	tags := getTags(expr)
	return tags, nil
}

func getTags(expr influxql.Expr) Tags {
	var result Tags
	var name *influxql.VarRef
	var value string
	var ok bool
loop:
	switch expr.(type) {
	case *influxql.ParenExpr:
		parenExpr := expr.(*influxql.ParenExpr)
		result = append(result, getTags(parenExpr.Expr)...)
	case *influxql.BinaryExpr:
		binaryExpr := expr.(*influxql.BinaryExpr)
		// 如果不是等号的操作，则需要继续递归左方和右方的所有内容
		if binaryExpr.Op != influxql.EQ {
			result = append(result, getTags(binaryExpr.LHS)...)
			result = append(result, getTags(binaryExpr.RHS)...)
			break
		}

		// 否则，此时是等号的操作，需要考虑将左方放入到维度中
		if name, ok = binaryExpr.LHS.(*influxql.VarRef); !ok {
			// 如果装换失败了，表示这个表达式不是简单的 A=B，对于我们的维度解析没有任何意义，放过它好了
			break
		}

		switch tempExpr := binaryExpr.RHS.(type) {
		// 右方表达式只能是：整形、字符串或者数字，否则不认
		// 太复杂的，我们二期见
		case *influxql.IntegerLiteral:
			value = fmt.Sprintf("%d", tempExpr.Val)
		case *influxql.NumberLiteral:
			value = fmt.Sprintf("%f", tempExpr.Val)
		case *influxql.StringLiteral:
			value = tempExpr.Val
		default:
			break loop
		}

		result = append(result, Tag{
			Key:   []byte(name.Val),
			Value: []byte(value),
		})
	}

	return result
}
