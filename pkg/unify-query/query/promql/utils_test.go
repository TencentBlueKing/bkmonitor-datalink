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

func TestWhereList_Check(t *testing.T) {
	whereList := NewWhereList()
	whereList.Append(AndOperator, NewWhere("bk_biz_id", "1", EqualOperator, StringType))
	whereList.Append(AndOperator, NewWhere("bk_biz_id", "2", EqualOperator, StringType))
	whereList.Append(AndOperator, NewWhere("other_field", "value", EqualOperator, StringType))

	t.Run("check existing tag value", func(t *testing.T) {
		assert.True(t, whereList.Check("bk_biz_id", []string{"1", "3"}))
		assert.True(t, whereList.Check("bk_biz_id", []string{"2", "3"}))
		assert.True(t, whereList.Check("bk_biz_id", []string{"1", "2"}))
	})

	t.Run("check non-existing tag value", func(t *testing.T) {
		assert.False(t, whereList.Check("bk_biz_id", []string{"3", "4"}))
		assert.False(t, whereList.Check("non_existing", []string{"1", "2"}))
	})

	t.Run("check with empty tag values", func(t *testing.T) {
		assert.False(t, whereList.Check("bk_biz_id", []string{}))
	})

	t.Run("check with non-equal operator", func(t *testing.T) {
		whereList2 := NewWhereList()
		whereList2.Append(AndOperator, NewWhere("bk_biz_id", "1", NEqualOperator, StringType))
		assert.False(t, whereList2.Check("bk_biz_id", []string{"1"}))
	})
}

func TestWhere_String(t *testing.T) {
	t.Run("string type", func(t *testing.T) {
		w := NewWhere("field_name", "field_value", EqualOperator, StringType)
		result := w.String()
		assert.Contains(t, result, "field_name")
		assert.Contains(t, result, "field_value")
		assert.Contains(t, result, "=")
	})

	t.Run("num type", func(t *testing.T) {
		w := NewWhere("time", "1000", UpperEqualOperator, NumType)
		result := w.String()
		assert.Contains(t, result, "time")
		assert.Contains(t, result, "1000")
		assert.Contains(t, result, ">=")
	})

	t.Run("regexp type", func(t *testing.T) {
		w := NewWhere("field", "test.*", RegexpOperator, RegexpType)
		result := w.String()
		assert.Contains(t, result, "field")
		assert.Contains(t, result, "test.*")
		assert.Contains(t, result, "=~")
	})

	t.Run("regexp type with slash", func(t *testing.T) {
		w := NewWhere("field", "test/path", RegexpOperator, RegexpType)
		result := w.String()
		assert.Contains(t, result, "\\/")
	})

	t.Run("text type", func(t *testing.T) {
		w := NewTextWhere("raw text value")
		result := w.String()
		assert.Equal(t, "raw text value", result)
	})
}

func TestNewWhere(t *testing.T) {
	w := NewWhere("test_field", "test_value", EqualOperator, StringType)
	assert.Equal(t, "test_field", w.Name)
	assert.Equal(t, "test_value", w.Value)
	assert.Equal(t, EqualOperator, w.Operator)
	assert.Equal(t, StringType, w.ValueType)
}

func TestNewTextWhere(t *testing.T) {
	w := NewTextWhere("raw text")
	assert.Equal(t, "", w.Name)
	assert.Equal(t, "raw text", w.Value)
	assert.Equal(t, "", w.Operator)
	assert.Equal(t, TextType, w.ValueType)
}

func TestGetSegmented(t *testing.T) {
	t.Run("disabled segmentation", func(t *testing.T) {
		opt := SegmentedOpt{
			Enable: false,
			Start:  1000,
			End:    5000,
		}
		segments := GetSegmented(opt)
		assert.Len(t, segments, 1)
		assert.Equal(t, int64(1000), segments[0].Start)
		assert.Equal(t, int64(5000), segments[0].End)
	})

	t.Run("normal segmentation", func(t *testing.T) {
		opt := SegmentedOpt{
			Enable:      true,
			Start:       1000,
			End:         5000,
			Interval:    1000,
			MinInterval: "1s",
			MaxRoutines: 10,
		}
		segments := GetSegmented(opt)
		assert.Greater(t, len(segments), 1)
		assert.Equal(t, int64(1000), segments[0].Start)
		assert.Equal(t, int64(5000), segments[len(segments)-1].End)
	})

	t.Run("invalid min interval", func(t *testing.T) {
		opt := SegmentedOpt{
			Enable:      true,
			Start:       1000,
			End:         5000,
			Interval:    1000,
			MinInterval: "invalid",
			MaxRoutines: 10,
		}
		segments := GetSegmented(opt)
		assert.Len(t, segments, 1)
		assert.Equal(t, int64(1000), segments[0].Start)
		assert.Equal(t, int64(5000), segments[0].End)
	})
}

func TestMakeAndConditions(t *testing.T) {
	t.Run("single condition", func(t *testing.T) {
		row := []ConditionField{
			{
				DimensionName: "field1",
				Value:         []string{"value1"},
				Operator:      EqualOperator,
			},
		}
		result := MakeAndConditions(row)
		assert.Contains(t, result, "field1")
		assert.Contains(t, result, "value1")
	})

	t.Run("multiple conditions", func(t *testing.T) {
		row := []ConditionField{
			{
				DimensionName: "field1",
				Value:         []string{"value1"},
				Operator:      EqualOperator,
			},
			{
				DimensionName: "field2",
				Value:         []string{"value2"},
				Operator:      NEqualOperator,
			},
		}
		result := MakeAndConditions(row)
		assert.Contains(t, result, "field1")
		assert.Contains(t, result, "field2")
		assert.Contains(t, result, "and")
	})
}
