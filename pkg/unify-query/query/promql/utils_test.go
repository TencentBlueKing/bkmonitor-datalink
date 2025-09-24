// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package promql

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestNewWhereList
func TestNewWhereList(t *testing.T) {
	conditions := [][]ConditionField{
		{
			{
				DimensionName: "database",
				Operator:      "!~",
				Value:         []string{"aaa"},
			},
			{
				DimensionName: "tag",
				Operator:      "=",
				Value:         []string{"3", "4"},
			},
			{
				DimensionName: "user",
				Operator:      "=",
				Value:         []string{"5", "6"},
			},
		},
		{
			{
				DimensionName: "name",
				Operator:      "=",
				Value:         []string{"7", "8"},
			},
			{
				DimensionName: "test1",
				Operator:      "=",
				Value:         []string{"9", "10"},
			},
		},
	}

	whereList := NewWhereList()
	whereList.Append(OrOperator, NewTextWhere(MakeOrExpression(conditions)))
	whereList.Append(AndOperator, NewWhere("database", "database", EqualOperator, StringType))
	whereList.Append(AndOperator, NewWhere("tag", "tag", NEqualOperator, StringType))
	whereList.Append(AndOperator, NewWhere("name", "name", RegexpOperator, RegexpType))
	whereList.Append(AndOperator, NewWhere("test2", "abcdefg.*", RegexpOperator, RegexpType))

	whereList.Append(AndOperator, NewWhere("time", "0", UpperEqualOperator, NumType))
	whereList.Append(AndOperator, NewWhere("time", "10000", LowerOperator, NumType))

	executed := `(("database"!~/aaa/ and (("tag"='3' or "tag"='4') and ("user"='5' or "user"='6'))) or (("name"='7' or "name"='8') and (test1='9' or test1='10'))) or "database" = 'database' and "tag" != 'tag' and "name" =~ /name/ and test2 =~ /abcdefg.*/ and time >= 0 and time < 10000`
	assert.Equal(t, executed, whereList.String())
}
