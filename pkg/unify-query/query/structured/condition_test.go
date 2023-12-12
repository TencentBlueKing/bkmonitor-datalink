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
	"errors"
	"fmt"
	"testing"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

// TestConditionListFieldAnalysis
func TestConditionListFieldAnalysis(t *testing.T) {
	log.InitTestLogger()

	var testCases = []struct {
		condition Conditions
		result    []int
		err       error
	}{
		// 长度不匹配
		{
			condition: Conditions{
				FieldList: []ConditionField{{
					DimensionName: "test1",
					Operator:      "==",
					Value:         []string{"abc"},
				}, {
					DimensionName: "test1",
					Operator:      "==",
					Value:         []string{"abc"},
				}},
				ConditionList: []string{},
			},
			result: nil,
			err:    errors.New("not match"),
		},
		// 简单的一个and拼接
		{
			condition: Conditions{
				FieldList: []ConditionField{{
					DimensionName: "test1",
					Operator:      "==",
					Value:         []string{"abc"},
				}, {
					DimensionName: "test1",
					Operator:      "==",
					Value:         []string{"abc"},
				}},
				ConditionList: []string{"and"},
			},
			result: []int{2},
		},
		// 简单的or拼接
		{
			condition: Conditions{
				FieldList: []ConditionField{{
					DimensionName: "test1",
					Operator:      "==",
					Value:         []string{"abc"},
				}, {
					DimensionName: "test1",
					Operator:      "==",
					Value:         []string{"abc"},
				}},
				ConditionList: []string{"or"},
			},
			result: []int{1, 1},
		},
		// and和or混合
		{
			condition: Conditions{
				FieldList: []ConditionField{{
					DimensionName: "test1",
					Operator:      "==",
					Value:         []string{"abc"},
				}, {
					DimensionName: "test1",
					Operator:      "==",
					Value:         []string{"abc"},
				}, {
					DimensionName: "test1",
					Operator:      "==",
					Value:         []string{"abc"},
				}},
				ConditionList: []string{"and", "or"},
			},
			result: []int{2, 1},
		},
		// and和or混合
		{
			condition: Conditions{
				FieldList: []ConditionField{{
					DimensionName: "test1",
					Operator:      "==",
					Value:         []string{"abc"},
				}, {
					DimensionName: "test1",
					Operator:      "==",
					Value:         []string{"abc"},
				}, {
					DimensionName: "test1",
					Operator:      "==",
					Value:         []string{"abc"},
				}},
				ConditionList: []string{"or", "and"},
			},
			result: []int{1, 2},
		},
	}

	for _, testCase := range testCases {
		testResult, err := testCase.condition.AnalysisConditions()
		if testCase.err != nil {
			assert.NotNil(t, err)
			continue
		}

		assert.Equal(t, len(testCase.result), len(testResult), "assert row")
		for row, columnLength := range testCase.result {
			assert.Equal(t, columnLength, len(testResult[row]), "row->[%d] assert column failed", row)
		}

	}
}

// TestConditionFieldOperatorToProm
func TestConditionFieldOperatorToProm(t *testing.T) {
	testData := []struct {
		testData   ConditionField
		exceptData labels.MatchType
	}{
		{
			testData:   ConditionField{Operator: ConditionEqual},
			exceptData: labels.MatchEqual,
		},
		{
			testData:   ConditionField{Operator: ConditionNotEqual},
			exceptData: labels.MatchNotEqual,
		},
		{
			testData:   ConditionField{Operator: ConditionRegEqual},
			exceptData: labels.MatchRegexp,
		},
		{
			testData:   ConditionField{Operator: ConditionNotRegEqual},
			exceptData: labels.MatchNotRegexp,
		},
		{
			testData:   ConditionField{Operator: ConditionContains},
			exceptData: labels.MatchRegexp,
		},
		{
			testData:   ConditionField{Operator: ConditionNotContains},
			exceptData: labels.MatchNotRegexp,
		},
	}

	for _, data := range testData {
		o := data.testData.ToPromOperator()
		assert.Equal(t, o, data.exceptData, "test with op->[%s]", data.testData.Operator)
	}
}

// TestConditionFieldToProm
func TestConditionFieldToProm(t *testing.T) {

	log.InitTestLogger()

	testData := []struct {
		condition Conditions
		labels    []*labels.Matcher
		fields    [][]ConditionField
		isErr     bool
	}{
		// 测试正常的过滤关系拼接
		{
			condition: Conditions{
				FieldList: []ConditionField{
					{
						DimensionName: "label1",
						Value:         []string{"value1"},
						Operator:      ConditionEqual,
					},
				},
			},
			labels: []*labels.Matcher{
				{
					Name:  "label1",
					Value: "value1",
					Type:  labels.MatchEqual,
				},
			},
			fields: nil,
			isErr:  false,
		},
		// 测试正则的匹配
		{
			condition: Conditions{
				FieldList: []ConditionField{
					{
						DimensionName: "label1",
						Value:         []string{"value1"},
						Operator:      ConditionRegEqual,
					},
				},
			},
			labels: []*labels.Matcher{
				{
					Name:  "label1",
					Value: "value1",
					Type:  labels.MatchRegexp,
				},
			},
			fields: nil,
			isErr:  false,
		},
		// 如果是有and拼接的，需要转换为label.Matcher
		{
			condition: Conditions{
				FieldList: []ConditionField{
					{
						DimensionName: "label1",
						Value:         []string{"value1"},
						Operator:      ConditionEqual,
					},
					{
						DimensionName: "label2",
						Value:         []string{"value2"},
						Operator:      ConditionEqual,
					},
				},
				ConditionList: []string{ConditionAnd},
			},
			labels: []*labels.Matcher{
				{
					Name:  "label1",
					Value: "value1",
					Type:  labels.MatchEqual,
				},
				{
					Name:  "label2",
					Value: "value2",
					Type:  labels.MatchEqual,
				},
			},
			fields: nil,
			isErr:  false,
		},
		// 如果是有or拼接的，需要转换为field
		{
			condition: Conditions{
				FieldList: []ConditionField{
					{
						DimensionName: "label1",
						Value:         []string{"value1"},
						Operator:      ConditionEqual,
					},
					{
						DimensionName: "label2",
						Value:         []string{"value2"},
						Operator:      ConditionEqual,
					},
				},
				ConditionList: []string{ConditionOr},
			},
			labels: nil,
			fields: [][]ConditionField{
				{
					{
						DimensionName: "label1",
						Value:         []string{"value1"},
						Operator:      ConditionEqual,
					},
				},
				{
					{
						DimensionName: "label2",
						Value:         []string{"value2"},
						Operator:      ConditionEqual,
					},
				},
			},
			isErr: false,
		},
	}

	for _, data := range testData {
		labelInfo, fields, err := data.condition.ToProm()
		// 如果预期是有错误，则先检查异常
		if data.isErr {
			assert.NotNil(t, err, "test err return not nil")
			// 由于有了错误，后面的就不用看了，直接下一条
			continue
		}

		// 如果预期是存在直接的返回结果，检查返回的内容
		if data.labels != nil {
			for index, targetLabel := range data.labels {
				assert.Equal(t, labelInfo[index].Name, targetLabel.Name, "normal label name match")
				assert.Equal(t, labelInfo[index].Value, targetLabel.Value, "normal label value match")
				assert.Equal(t, labelInfo[index].Type, targetLabel.Type, "normal label match type")
			}
			assert.Nil(t, fields)
			continue
		}

		// 如果预期是返回or的拼接，遍历检查内容是否符合预期
		assert.NotNil(t, fields)
		for rowIndex, fieldList := range fields {
			for columnIndex, field := range fieldList {
				f := data.fields[rowIndex][columnIndex]
				assert.Equal(t, f.DimensionName, field.DimensionName)
				assert.Equal(t, f.Value, field.Value)
				assert.Equal(t, f.Operator, field.Operator)
			}
		}
	}
}

// TestConditions_GetRequiredField
func TestConditions_GetRequiredField(t *testing.T) {
	log.InitTestLogger()

	testCases := map[string]struct {
		condition  *Conditions
		bizIDs     []int
		projectIDs []string
		clusterIDs []string
		err        error
	}{
		"normal": {
			condition: &Conditions{
				FieldList: []ConditionField{
					{
						DimensionName: BizID,
						Value:         []string{"2"},
						Operator:      "eq",
					},
					{
						DimensionName: ClusterID,
						Value:         []string{"bcs-k8s", "bcs-k9s"},
						Operator:      "contains",
					},
				},
			},
			bizIDs:     []int{2},
			clusterIDs: []string{"bcs-k8s", "bcs-k9s"},
		},
		"unsupport cluster op": {
			condition: &Conditions{
				FieldList: []ConditionField{
					{
						DimensionName: BizID,
						Value:         []string{"2"},
						Operator:      "eq",
					},
					{
						DimensionName: ClusterID,
						Value:         []string{"bcs-k8s"},
						Operator:      "reg",
					},
				},
			},
			bizIDs: []int{2},
		},
		"unsupport biz op": {
			condition: &Conditions{
				FieldList: []ConditionField{
					{
						DimensionName: BizID,
						Value:         []string{"2"},
						Operator:      "reg",
					},
					{
						DimensionName: ClusterID,
						Value:         []string{"bcs-k8s"},
						Operator:      "reg",
					},
				},
			},
			err: fmt.Errorf("unsupport operations to filter %s, "+
				"only support %s, %s", BizID, ConditionEqual, ConditionContains),
		},
	}

	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			bizIDs, projectIDs, clusterIDs, err := testCase.condition.GetRequiredFiled()
			assert.Equal(t, testCase.bizIDs, bizIDs, name)
			assert.Equal(t, testCase.projectIDs, projectIDs, name)
			assert.Equal(t, testCase.clusterIDs, clusterIDs, name)
			assert.Equal(t, testCase.err, err, name)
		})
	}

}

// TestConditionField_LabelMatcherConvert
func TestConditionField_LabelMatcherConvert(t *testing.T) {
	testCases := map[string]struct {
		matches string
		expect  []ConditionField
		err     error
	}{
		"normal": {
			matches: `m1{tag1="i",t2!="cc",t3=~"ooo"}`,
			expect: []ConditionField{
				{
					DimensionName: "tag1",
					Operator:      ConditionEqual,
					Value:         []string{"i"},
				},
				{
					DimensionName: "t2",
					Operator:      ConditionNotEqual,
					Value:         []string{"cc"},
				},
				{
					DimensionName: "t3",
					Operator:      ConditionRegEqual,
					Value:         []string{"ooo"},
				},
			},
		},
	}

	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			lm, err := parser.ParseMetricSelector(testCase.matches)
			assert.NoError(t, err)
			_, conds, err := LabelMatcherToConditions(lm)
			assert.Equal(t, testCase.expect, conds)
			if testCase.err != nil {
				assert.Error(t, testCase.err, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestAllConditions_VMString(t *testing.T) {
	for i, c := range []struct {
		allConditions AllConditions
		vmCondition   string
		isRegex       bool
		metric        string
		rt            string
	}{
		{
			allConditions: AllConditions{
				{
					{
						DimensionName: "dim-1",
						Value: []string{
							"val-1",
							"val-2",
						},
						Operator: ConditionEqual,
					},
					{
						DimensionName: "dim-2",
						Value: []string{
							"val-4",
							"val-5",
						},
						Operator: ConditionContains,
					},
					{
						DimensionName: "dim-3",
						Value: []string{
							"val-8",
							"val-9",
						},
						Operator: ConditionRegEqual,
					},
				},
				{
					{
						DimensionName: "dim1-1",
						Value: []string{
							"val-1",
						},
						Operator: ConditionNotEqual,
					},
					{
						DimensionName: "dim1-2",
						Value: []string{
							"val-5",
						},
						Operator: ConditionNotContains,
					},
					{
						DimensionName: "dim1-3",
						Value: []string{
							"val-8",
						},
						Operator: ConditionRegEqual,
					},
				},
			},
			metric:      "metric_1",
			rt:          "rt-n",
			vmCondition: `dim-1=~"^(val-1|val-2)$", dim-2=~"^(val-4|val-5)$", dim-3=~"val-8|val-9", result_table_id="rt-n", __name__="metric_1" or dim1-1!="val-1", dim1-2!="val-5", dim1-3=~"val-8", result_table_id="rt-n", __name__="metric_1"`,
		},
		{
			allConditions: AllConditions{
				{
					{
						DimensionName: "dim-1",
						Value: []string{
							"val-1",
							"val-2",
						},
						Operator: ConditionContains,
					},
					{
						DimensionName: "dim-2",
						Value: []string{
							"val-1",
							"val-2",
						},
						Operator: ConditionNotContains,
					},
				},
			},
			metric:      "metric_.*",
			rt:          "rt-n",
			isRegex:     true,
			vmCondition: `dim-1=~"^(val-1|val-2)$", dim-2!~"^(val-1|val-2)$", result_table_id="rt-n", __name__=~"metric_.*"`,
		},
		{
			allConditions: AllConditions{},
			metric:        "",
			rt:            "rt-n",
			vmCondition:   `result_table_id="rt-n"`,
		},
		{
			allConditions: AllConditions{},
			metric:        "metric",
			vmCondition:   `__name__="metric"`,
		},
		{
			allConditions: AllConditions{},
			metric:        "",
			vmCondition:   ``,
		},
		{
			allConditions: AllConditions{
				{
					{
						DimensionName: "dim-1",
						Value: []string{
							"val-1",
							"val-2",
						},
						Operator: ConditionContains,
					},
				},
				{
					{
						DimensionName: "dim-2",
						Value: []string{
							"val-1",
							"val-2",
						},
						Operator: ConditionNotContains,
					},
				},
			},
			metric:      "metric_.*",
			rt:          "rt-n",
			isRegex:     true,
			vmCondition: `dim-1=~"^(val-1|val-2)$", result_table_id="rt-n", __name__=~"metric_.*" or dim-2!~"^(val-1|val-2)$", result_table_id="rt-n", __name__=~"metric_.*"`,
		},
	} {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			actual, _ := c.allConditions.VMString(c.rt, c.metric, c.isRegex)
			assert.Equal(t, c.vmCondition, actual)
		})
	}
}
