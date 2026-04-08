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
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
)

// TestConditionListFieldAnalysis
func TestConditionListFieldAnalysis(t *testing.T) {
	mock.Init()

	testCases := []struct {
		condition Conditions
		result    []int
		vm        metadata.VmCondition
		sql       string
		err       error
	}{
		// value 为空
		{
			condition: Conditions{
				FieldList: []ConditionField{
					{
						DimensionName: "test1",
						Operator:      ConditionContains,
						Value:         []string{"abc"},
					},
					{
						DimensionName: "test2",
						Operator:      ConditionEqual,
						Value:         []string{},
					},
					{
						DimensionName: "test3",
						Operator:      ConditionEqual,
						Value:         []string{"det"},
					},
				},
				ConditionList: []string{"and", "and"},
			},
			result: []int{2},
			sql:    "(`test1` = 'abc' and `test3` = 'det')",
			vm:     `test1="abc", test3="det", result_table_id="table_id"`,
		},
		// 长度不匹配
		{
			condition: Conditions{
				FieldList: []ConditionField{{
					DimensionName: "test1",
					Operator:      ConditionContains,
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
				FieldList: []ConditionField{
					{
						DimensionName: "test1",
						Operator:      ConditionContains,
						Value:         []string{"abc"},
					},
					{
						DimensionName: "test1",
						Operator:      ConditionNotContains,
						Value:         []string{"abc"},
					},
				},
				ConditionList: []string{"and"},
			},
			result: []int{2},
			sql:    "(`test1` = 'abc' and `test1` != 'abc')",
			vm:     `test1="abc", test1!="abc", result_table_id="table_id"`,
		},
		// 简单的or拼接
		{
			condition: Conditions{
				FieldList: []ConditionField{
					{
						DimensionName: "test1",
						Operator:      ConditionRegEqual,
						Value:         []string{"abc"},
					},
					{
						DimensionName: "test2",
						Operator:      ConditionContains,
						Value:         []string{},
					},
					{
						DimensionName: "test1",
						Operator:      ConditionNotRegEqual,
						Value:         []string{"b", "c", "d"},
					},
				},
				ConditionList: []string{"or", "or"},
			},
			result: []int{1, 1},
			sql:    "`test1` REGEXP 'abc' or (`test1` NOT REGEXP 'b' and `test1` NOT REGEXP 'c' and `test1` NOT REGEXP 'd')",
			vm:     `test1=~"abc", result_table_id="table_id" or test1!~"b|c|d", result_table_id="table_id"`,
		},
		// and和or混合
		{
			condition: Conditions{
				FieldList: []ConditionField{{
					DimensionName: "test1",
					Operator:      ConditionContains,
					Value:         []string{"abc"},
				}, {
					DimensionName: "test1",
					Operator:      ConditionContains,
					Value:         []string{"abc", "bcd"},
				}, {
					DimensionName: "test1",
					Operator:      ConditionContains,
					Value:         []string{"abc", "ggg"},
				}},
				ConditionList: []string{"and", "or"},
			},
			result: []int{2, 1},
			sql:    "(`test1` = 'abc' and (`test1` = 'abc' or `test1` = 'bcd')) or (`test1` = 'abc' or `test1` = 'ggg')",
			vm:     `test1="abc", test1=~"^(abc|bcd)$", result_table_id="table_id" or test1=~"^(abc|ggg)$", result_table_id="table_id"`,
		},
		// and和or混合
		{
			condition: Conditions{
				FieldList: []ConditionField{{
					DimensionName: "test1",
					Operator:      ConditionContains,
					Value:         []string{"abc"},
				}, {
					DimensionName: "test1",
					Operator:      ConditionContains,
					Value:         []string{"abc"},
				}, {
					DimensionName: "test1",
					Operator:      ConditionContains,
					Value:         []string{"abc"},
				}},
				ConditionList: []string{"or", "and"},
			},
			result: []int{1, 2},
			sql:    "`test1` = 'abc' or (`test1` = 'abc' and `test1` = 'abc')",
			vm:     `test1="abc", result_table_id="table_id" or test1="abc", test1="abc", result_table_id="table_id"`,
		},
		{
			condition: Conditions{
				FieldList: []ConditionField{
					{
						DimensionName: "job",
						Operator:      ConditionContains,
						Value:         []string{"kube-state-metrics"},
					},
					{
						DimensionName: "namespace",
						Operator:      ConditionNotContains,
						Value:         []string{""},
					},
					{
						DimensionName: "pod_name",
						Operator:      ConditionNotContains,
						Value:         []string{""},
					},
					{
						DimensionName: "bcs_cluster_id",
						Operator:      ConditionRegEqual,
						Value:         []string{"BCS-K8S-40822", "BCS-K8S-40839", "BCS-K8S-40840", "BCS-K8S-40989", "BCS-K8S-41105", "BCS-K8S-41106"},
					},
					{
						DimensionName: "container",
						Operator:      ConditionNotContains,
						Value:         []string{"fluentd"},
					},
				},
				ConditionList: []string{"and", "and", "and", "and"},
			},
			result: []int{5},
			sql:    "(`job` = 'kube-state-metrics' and `namespace` != '' and `pod_name` != '' and (`bcs_cluster_id` REGEXP 'BCS-K8S-40822' or `bcs_cluster_id` REGEXP 'BCS-K8S-40839' or `bcs_cluster_id` REGEXP 'BCS-K8S-40840' or `bcs_cluster_id` REGEXP 'BCS-K8S-40989' or `bcs_cluster_id` REGEXP 'BCS-K8S-41105' or `bcs_cluster_id` REGEXP 'BCS-K8S-41106') and `container` != 'fluentd')",
			vm:     `job="kube-state-metrics", namespace!="", pod_name!="", bcs_cluster_id=~"BCS-K8S-40822|BCS-K8S-40839|BCS-K8S-40840|BCS-K8S-40989|BCS-K8S-41105|BCS-K8S-41106", container!="fluentd", result_table_id="table_id"`,
		},
		{
			condition: Conditions{
				FieldList: []ConditionField{
					{
						DimensionName: "p1",
						Operator:      ConditionContains,
						Value:         []string{`{"moduleType":3}`},
					},
				},
			},
			result: []int{1},
			sql:    "`p1` = '{\"moduleType\":3}'",
			vm:     `p1="{\"moduleType\":3}", result_table_id="table_id"`,
		},
		{
			condition: Conditions{
				FieldList: []ConditionField{
					{
						DimensionName: "say",
						Operator:      ConditionContains,
						Value:         []string{`What's this?`},
					},
				},
			},
			result: []int{1},
			sql:    "`say` = 'What''s this?'",
			vm:     `say="What's this?", result_table_id="table_id"`,
		},
	}

	for idx, testCase := range testCases {
		t.Run(fmt.Sprintf("%d", idx), func(t *testing.T) {
			testResult, err := testCase.condition.AnalysisConditions()

			if testCase.err != nil {
				assert.NotNil(t, err)
				return
			}

			assert.Equal(t, len(testCase.result), len(testResult), "assert row")
			for row, columnLength := range testCase.result {
				assert.Equal(t, columnLength, len(testResult[row]), "row->[%d] assert column failed", row)
			}

			vmCondition, _ := testResult.VMString("table_id", "", false)
			assert.Equal(t, testCase.vm, vmCondition)

			sqlCondtion := testResult.BkSql()
			assert.Equal(t, testCase.sql, sqlCondtion)
		})
	}
}

func TestConditionCompare(t *testing.T) {
	testCases := map[string]struct {
		condition Conditions
		expected  bool
	}{
		"cluster two or not reg one": {
			condition: Conditions{
				FieldList: []ConditionField{
					{
						DimensionName: "bcs_cluster_id",
						Operator:      ConditionNotRegEqual,
						Value:         []string{`k8s`},
					},
					{
						DimensionName: "bcs_cluster_id",
						Operator:      ConditionContains,
						Value:         []string{`k8s-2`},
					},
				},
				ConditionList: []string{"or"},
			},
			expected: false,
		},
		"cluster two or not reg": {
			condition: Conditions{
				FieldList: []ConditionField{
					{
						DimensionName: "bcs_cluster_id",
						Operator:      ConditionNotRegEqual,
						Value:         []string{`k8s`},
					},
					{
						DimensionName: "bcs_cluster_id",
						Operator:      ConditionContains,
						Value:         []string{`k8s-1`},
					},
				},
				ConditionList: []string{"or"},
			},
			expected: true,
		},
		"cluster one contains": {
			condition: Conditions{
				FieldList: []ConditionField{
					{
						DimensionName: "bcs_cluster_id",
						Operator:      ConditionContains,
						Value:         []string{`k8s-2`},
					},
				},
			},
			expected: false,
		},
		"cluster two or not contains": {
			condition: Conditions{
				FieldList: []ConditionField{
					{
						DimensionName: "bcs_cluster_id",
						Operator:      ConditionNotContains,
						Value:         []string{`k8s-1`},
					},
					{
						DimensionName: "bcs_cluster_id",
						Operator:      ConditionContains,
						Value:         []string{`k8s-2`},
					},
				},
				ConditionList: []string{"or"},
			},
			expected: false,
		},
		"cluster two or req": {
			condition: Conditions{
				FieldList: []ConditionField{
					{
						DimensionName: "bcs_cluster_id",
						Operator:      ConditionRegEqual,
						Value:         []string{`k8s`},
					},
					{
						DimensionName: "bcs_cluster_id",
						Operator:      ConditionContains,
						Value:         []string{`k8s-2`},
					},
				},
				ConditionList: []string{"or"},
			},
			expected: true,
		},
		"cluster two or contains": {
			condition: Conditions{
				FieldList: []ConditionField{
					{
						DimensionName: "bcs_cluster_id",
						Operator:      ConditionContains,
						Value:         []string{`k8s-1`},
					},
					{
						DimensionName: "bcs_cluster_id",
						Operator:      ConditionContains,
						Value:         []string{`k8s-2`},
					},
				},
				ConditionList: []string{"or"},
			},
			expected: true,
		},
		"cluster two and contains": {
			condition: Conditions{
				FieldList: []ConditionField{
					{
						DimensionName: "bcs_cluster_id",
						Operator:      ConditionContains,
						Value:         []string{`k8s-1`},
					},
					{
						DimensionName: "bcs_cluster_id",
						Operator:      ConditionContains,
						Value:         []string{`k8s-2`},
					},
				},
				ConditionList: []string{"and"},
			},
			expected: false,
		},
		"cluster two or contains multi values": {
			condition: Conditions{
				FieldList: []ConditionField{
					{
						DimensionName: "bcs_cluster_id",
						Operator:      ConditionContains,
						Value:         []string{`k8s-1`, `k8s-2`},
					},
					{
						DimensionName: "bcs_cluster_id",
						Operator:      ConditionContains,
						Value:         []string{`k8s-3`},
					},
				},
				ConditionList: []string{"or"},
			},
			expected: true,
		},
		"cluster three or contains multi values": {
			condition: Conditions{
				FieldList: []ConditionField{
					{
						DimensionName: "bcs_cluster_id",
						Operator:      ConditionContains,
						Value:         []string{`k8s-1`, `k8s-2`},
					},
					{
						DimensionName: "bcs_cluster_id",
						Operator:      ConditionContains,
						Value:         []string{`k8s-3`},
					},
					{
						DimensionName: "bcs_cluster_id",
						Operator:      ConditionContains,
						Value:         []string{`k8s-1`},
					},
				},
				ConditionList: []string{"and", "or"},
			},
			expected: true,
		},
		"cluster and ns two or contains multi values": {
			condition: Conditions{
				FieldList: []ConditionField{
					{
						DimensionName: "bcs_cluster_id",
						Operator:      ConditionContains,
						Value:         []string{`k8s-1`, `k8s-2`},
					},
					{
						DimensionName: "namespace",
						Operator:      ConditionContains,
						Value:         []string{`ns-1`},
					},
					{
						DimensionName: "bcs_cluster_id",
						Operator:      ConditionContains,
						Value:         []string{`k8s-3`},
					},
					{
						DimensionName: "namespace",
						Operator:      ConditionContains,
						Value:         []string{`ns-2`},
					},
				},
				ConditionList: []string{"and", "or", "and"},
			},
			expected: true,
		},
		"cluster with empty": {
			condition: Conditions{},
			expected:  true,
		},
		"cluster with nil": {
			expected: true,
		},
	}

	checkCluster := "k8s-1"

	for name, c := range testCases {
		t.Run(name, func(t *testing.T) {
			allConditions, err := c.condition.AnalysisConditions()
			if err != nil {
				panic(err)
			}
			res, err := allConditions.Compare(ClusterID, checkCluster)
			if err != nil {
				panic(err)
			}
			assert.Equal(t, c.expected, res)
		})
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
		vmCondition   metadata.VmCondition
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

// TestAllConditions_MatchResultTableLabels 表标签匹配：空条件、单 eq、缺 key、OR 组、AND 组（表标签路由统一使用 AllConditions.MatchResultTableLabels）
func TestAllConditions_MatchResultTableLabels(t *testing.T) {
	t.Run("nil_empty", func(t *testing.T) {
		var c AllConditions
		ok, err := c.MatchResultTableLabels(map[string]string{"a": "1"})
		assert.NoError(t, err)
		assert.True(t, ok)
		ok, err = AllConditions{}.MatchResultTableLabels(map[string]string{"a": "1"})
		assert.NoError(t, err)
		assert.True(t, ok)
	})
	t.Run("single_eq_match", func(t *testing.T) {
		c := AllConditions{{{DimensionName: "scene", Value: []string{"log"}, Operator: ConditionEqual}}}
		ok, err := c.MatchResultTableLabels(map[string]string{"scene": "log"})
		assert.NoError(t, err)
		assert.True(t, ok)
		ok, err = c.MatchResultTableLabels(map[string]string{"scene": "k8s"})
		assert.NoError(t, err)
		assert.False(t, ok)
	})
	t.Run("key_missing", func(t *testing.T) {
		c := AllConditions{{{DimensionName: "scene", Value: []string{"log"}, Operator: ConditionEqual}}}
		ok, err := c.MatchResultTableLabels(map[string]string{})
		assert.NoError(t, err)
		assert.False(t, ok)
		ok, err = c.MatchResultTableLabels(map[string]string{"other": "x"})
		assert.NoError(t, err)
		assert.False(t, ok)
	})
	t.Run("or_groups", func(t *testing.T) {
		c := AllConditions{
			{{DimensionName: "scene", Value: []string{"log"}, Operator: ConditionEqual}},
			{{DimensionName: "scene", Value: []string{"k8s"}, Operator: ConditionEqual}},
		}
		ok, err := c.MatchResultTableLabels(map[string]string{"scene": "log"})
		assert.NoError(t, err)
		assert.True(t, ok)
		ok, err = c.MatchResultTableLabels(map[string]string{"scene": "k8s"})
		assert.NoError(t, err)
		assert.True(t, ok)
		ok, err = c.MatchResultTableLabels(map[string]string{"scene": "other"})
		assert.NoError(t, err)
		assert.False(t, ok)
	})
	t.Run("and_group", func(t *testing.T) {
		c := AllConditions{{
			{DimensionName: "scene", Value: []string{"log"}, Operator: ConditionEqual},
			{DimensionName: "cluster_id", Value: []string{"1"}, Operator: ConditionEqual},
		}}
		ok, err := c.MatchResultTableLabels(map[string]string{"scene": "log", "cluster_id": "1"})
		assert.NoError(t, err)
		assert.True(t, ok)
		ok, err = c.MatchResultTableLabels(map[string]string{"scene": "log", "cluster_id": "2"})
		assert.NoError(t, err)
		assert.False(t, ok)
	})

	// 复杂情形：ne / req / nreq、多组 OR 内多条件 AND、混合操作符
	t.Run("single_ne", func(t *testing.T) {
		c := AllConditions{{{DimensionName: "scene", Value: []string{"metric"}, Operator: ConditionNotEqual}}}
		ok, err := c.MatchResultTableLabels(map[string]string{"scene": "log"})
		assert.NoError(t, err)
		assert.True(t, ok)
		ok, err = c.MatchResultTableLabels(map[string]string{"scene": "metric"})
		assert.NoError(t, err)
		assert.False(t, ok)
		// 缺 scene 与 PromQL scene!="metric" 一致：无该 label 时通过
		ok, err = c.MatchResultTableLabels(map[string]string{})
		assert.NoError(t, err)
		assert.True(t, ok)
		ok, err = c.MatchResultTableLabels(map[string]string{"other": "x"})
		assert.NoError(t, err)
		assert.True(t, ok)
	})
	t.Run("single_req", func(t *testing.T) {
		c := AllConditions{{{DimensionName: "scene", Value: []string{"log.*"}, Operator: ConditionRegEqual}}}
		ok, err := c.MatchResultTableLabels(map[string]string{"scene": "log"})
		assert.NoError(t, err)
		assert.True(t, ok)
		ok, err = c.MatchResultTableLabels(map[string]string{"scene": "log-api"})
		assert.NoError(t, err)
		assert.True(t, ok)
		ok, err = c.MatchResultTableLabels(map[string]string{"scene": "k8s"})
		assert.NoError(t, err)
		assert.False(t, ok)
	})
	t.Run("single_nreq", func(t *testing.T) {
		c := AllConditions{{{DimensionName: "scene", Value: []string{"metric.*"}, Operator: ConditionNotRegEqual}}}
		ok, err := c.MatchResultTableLabels(map[string]string{"scene": "log"})
		assert.NoError(t, err)
		assert.True(t, ok)
		ok, err = c.MatchResultTableLabels(map[string]string{"scene": "metric-api"})
		assert.NoError(t, err)
		assert.False(t, ok)
		ok, err = c.MatchResultTableLabels(map[string]string{}) // 缺 scene 与 !~ 负向语义一致：通过
		assert.NoError(t, err)
		assert.True(t, ok)
		ok, err = c.MatchResultTableLabels(map[string]string{"other": "x"})
		assert.NoError(t, err)
		assert.True(t, ok)
	})
	// nreq 多正则、非法正则、与其它条件 AND/OR 组合
	t.Run("nreq", func(t *testing.T) {
		t.Run("multiple_patterns_any_match_fails", func(t *testing.T) {
			c := AllConditions{{{DimensionName: "scene", Value: []string{"metric.*", "k8s.*"}, Operator: ConditionNotRegEqual}}}
			ok, err := c.MatchResultTableLabels(map[string]string{"scene": "log"})
			require.NoError(t, err)
			assert.True(t, ok, "不匹配任一排除正则则通过")
			ok, err = c.MatchResultTableLabels(map[string]string{"scene": "metric-api"})
			require.NoError(t, err)
			assert.False(t, ok, "命中第一个排除正则则失败")
			ok, err = c.MatchResultTableLabels(map[string]string{"scene": "k8s-1"})
			require.NoError(t, err)
			assert.False(t, ok, "命中第二个排除正则则失败")
		})
		t.Run("invalid_regex_returns_error", func(t *testing.T) {
			c := AllConditions{{{DimensionName: "scene", Value: []string{"("}, Operator: ConditionNotRegEqual}}}
			ok, err := c.MatchResultTableLabels(map[string]string{"scene": "log"})
			require.Error(t, err)
			assert.False(t, ok)
		})
		t.Run("and_group_eq_plus_nreq_missing_second_label_passes", func(t *testing.T) {
			// scene=log 且 cluster_id !~ ^tmp-.* ；无 cluster_id 时 nreq 通过
			c := AllConditions{{
				{DimensionName: "scene", Value: []string{"log"}, Operator: ConditionEqual},
				{DimensionName: "cluster_id", Value: []string{"^tmp-.*"}, Operator: ConditionNotRegEqual},
			}}
			ok, err := c.MatchResultTableLabels(map[string]string{"scene": "log"})
			require.NoError(t, err)
			assert.True(t, ok)
			ok, err = c.MatchResultTableLabels(map[string]string{"scene": "log", "cluster_id": "prod-1"})
			require.NoError(t, err)
			assert.True(t, ok)
			ok, err = c.MatchResultTableLabels(map[string]string{"scene": "log", "cluster_id": "tmp-1"})
			require.NoError(t, err)
			assert.False(t, ok)
		})
		t.Run("or_groups_any_branch_passes", func(t *testing.T) {
			// (scene !~ alpha.*) OR (scene !~ beta.*)
			c := AllConditions{
				{{DimensionName: "scene", Value: []string{"alpha.*"}, Operator: ConditionNotRegEqual}},
				{{DimensionName: "scene", Value: []string{"beta.*"}, Operator: ConditionNotRegEqual}},
			}
			ok, err := c.MatchResultTableLabels(map[string]string{"scene": "log"})
			require.NoError(t, err)
			assert.True(t, ok, "第一组即可满足")
			ok, err = c.MatchResultTableLabels(map[string]string{"scene": "alpha-1"})
			require.NoError(t, err)
			assert.True(t, ok, "第一组失败，第二组仍满足")
		})
		t.Run("or_groups_all_branches_fail", func(t *testing.T) {
			c := AllConditions{
				{{DimensionName: "scene", Value: []string{".*"}, Operator: ConditionNotRegEqual}},
				{{DimensionName: "scene", Value: []string{".*"}, Operator: ConditionNotRegEqual}},
			}
			ok, err := c.MatchResultTableLabels(map[string]string{"scene": "any"})
			require.NoError(t, err)
			assert.False(t, ok, "两组都是「不能匹配任意串」，有 scene 时皆失败")
		})
	})
	t.Run("and_group_with_ne", func(t *testing.T) {
		c := AllConditions{{
			{DimensionName: "scene", Value: []string{"log"}, Operator: ConditionEqual},
			{DimensionName: "env", Value: []string{"prod"}, Operator: ConditionNotEqual},
		}}
		ok, err := c.MatchResultTableLabels(map[string]string{"scene": "log", "env": "staging"})
		assert.NoError(t, err)
		assert.True(t, ok)
		ok, err = c.MatchResultTableLabels(map[string]string{"scene": "log", "env": "prod"})
		assert.NoError(t, err)
		assert.False(t, ok)
		// 仅有 scene=log、无 env：env!=prod 在缺 env 时通过
		ok, err = c.MatchResultTableLabels(map[string]string{"scene": "log"})
		assert.NoError(t, err)
		assert.True(t, ok)
		ok, err = c.MatchResultTableLabels(map[string]string{"scene": "k8s", "env": "staging"})
		assert.NoError(t, err)
		assert.False(t, ok)
	})
	t.Run("or_groups_with_and_each", func(t *testing.T) {
		// (scene=log AND cluster_id=1) OR (scene=k8s)
		c := AllConditions{
			{
				{DimensionName: "scene", Value: []string{"log"}, Operator: ConditionEqual},
				{DimensionName: "cluster_id", Value: []string{"1"}, Operator: ConditionEqual},
			},
			{{DimensionName: "scene", Value: []string{"k8s"}, Operator: ConditionEqual}},
		}
		ok, err := c.MatchResultTableLabels(map[string]string{"scene": "log", "cluster_id": "1"})
		assert.NoError(t, err)
		assert.True(t, ok)
		ok, err = c.MatchResultTableLabels(map[string]string{"scene": "k8s"})
		assert.NoError(t, err)
		assert.True(t, ok)
		ok, err = c.MatchResultTableLabels(map[string]string{"scene": "log", "cluster_id": "2"}) // 第一组不满足
		assert.NoError(t, err)
		assert.False(t, ok)
		ok, err = c.MatchResultTableLabels(map[string]string{"scene": "other"})
		assert.NoError(t, err)
		assert.False(t, ok)
	})
	t.Run("and_group_with_req", func(t *testing.T) {
		c := AllConditions{{
			{DimensionName: "scene", Value: []string{"log"}, Operator: ConditionEqual},
			{DimensionName: "cluster_id", Value: []string{"BCS-.*"}, Operator: ConditionRegEqual},
		}}
		ok, err := c.MatchResultTableLabels(map[string]string{"scene": "log", "cluster_id": "BCS-K8S-00001"})
		assert.NoError(t, err)
		assert.True(t, ok)
		ok, err = c.MatchResultTableLabels(map[string]string{"scene": "log", "cluster_id": "other"})
		assert.NoError(t, err)
		assert.False(t, ok)
	})
	t.Run("three_or_groups", func(t *testing.T) {
		c := AllConditions{
			{{DimensionName: "scene", Value: []string{"log"}, Operator: ConditionEqual}},
			{{DimensionName: "scene", Value: []string{"k8s"}, Operator: ConditionEqual}},
			{{DimensionName: "scene", Value: []string{"metric"}, Operator: ConditionEqual}},
		}
		for _, scene := range []string{"log", "k8s", "metric"} {
			ok, err := c.MatchResultTableLabels(map[string]string{"scene": scene})
			assert.NoError(t, err)
			assert.True(t, ok)
		}
		ok, err := c.MatchResultTableLabels(map[string]string{"scene": "other"})
		assert.NoError(t, err)
		assert.False(t, ok)
	})
}

// TestAllConditions_MatchesResultTableLabels 表标签过滤：仅返回 bool，空或 nil 视为通过（与 MatchResultTableLabels 语义一致）。
func TestAllConditions_MatchesResultTableLabels(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		assert.True(t, AllConditions(nil).MatchesResultTableLabels(map[string]string{"a": "1"}))
		assert.True(t, AllConditions{}.MatchesResultTableLabels(map[string]string{"a": "1"}))
	})
	t.Run("match_single", func(t *testing.T) {
		all := AllConditions{{{DimensionName: "scene", Value: []string{"log"}, Operator: ConditionEqual}}}
		assert.True(t, all.MatchesResultTableLabels(map[string]string{"scene": "log"}))
		assert.False(t, all.MatchesResultTableLabels(map[string]string{"scene": "k8s"}))
	})
	t.Run("or_groups", func(t *testing.T) {
		all := AllConditions{
			{{DimensionName: "scene", Value: []string{"log"}, Operator: ConditionEqual}},
			{{DimensionName: "scene", Value: []string{"k8s"}, Operator: ConditionEqual}},
		}
		assert.True(t, all.MatchesResultTableLabels(map[string]string{"scene": "log"}))
		assert.True(t, all.MatchesResultTableLabels(map[string]string{"scene": "k8s"}))
		assert.False(t, all.MatchesResultTableLabels(map[string]string{"scene": "other"}))
	})
}

func TestAllConditions_ToPromMatchers(t *testing.T) {
	ctx := context.Background()

	t.Run("nil", func(t *testing.T) {
		ms, err := AllConditions(nil).ToPromMatchers(ctx, nil)
		assert.NoError(t, err)
		assert.Nil(t, ms)
	})
	t.Run("empty", func(t *testing.T) {
		ms, err := AllConditions{}.ToPromMatchers(ctx, nil)
		assert.NoError(t, err)
		assert.Nil(t, ms)
	})
	t.Run("empty_group", func(t *testing.T) {
		ms, err := AllConditions{{}}.ToPromMatchers(ctx, nil)
		assert.NoError(t, err)
		assert.Nil(t, ms)
	})
	t.Run("single_eq", func(t *testing.T) {
		all := AllConditions{
			{{DimensionName: "scene", Value: []string{"log"}, Operator: ConditionEqual}},
		}
		ms, err := all.ToPromMatchers(ctx, nil)
		require.NoError(t, err)
		require.Len(t, ms, 1)
		assert.Equal(t, "__bk_query_label_selector_scene", ms[0].Name)
		assert.Equal(t, labels.MatchEqual, ms[0].Type)
		assert.Equal(t, "log", ms[0].Value)
	})
	t.Run("multi_and", func(t *testing.T) {
		all := AllConditions{{
			{DimensionName: "scene", Value: []string{"log"}, Operator: ConditionEqual},
			{DimensionName: "cluster_id", Value: []string{"BCS-.*"}, Operator: ConditionRegEqual},
		}}
		ms, err := all.ToPromMatchers(ctx, nil)
		require.NoError(t, err)
		require.Len(t, ms, 2)
		assert.Equal(t, labels.MatchEqual, ms[0].Type)
		assert.Equal(t, labels.MatchRegexp, ms[1].Type)
		assert.Equal(t, "BCS-.*", ms[1].Value)
	})
	t.Run("neq_nreg", func(t *testing.T) {
		all := AllConditions{{
			{DimensionName: "env", Value: []string{"prod"}, Operator: ConditionNotEqual},
			{DimensionName: "zone", Value: []string{"test.*"}, Operator: ConditionNotRegEqual},
		}}
		ms, err := all.ToPromMatchers(ctx, nil)
		require.NoError(t, err)
		require.Len(t, ms, 2)
		assert.Equal(t, labels.MatchNotEqual, ms[0].Type)
		assert.Equal(t, labels.MatchNotRegexp, ms[1].Type)
	})
	t.Run("or_groups_error", func(t *testing.T) {
		all := AllConditions{
			{{DimensionName: "scene", Value: []string{"log"}, Operator: ConditionEqual}},
			{{DimensionName: "scene", Value: []string{"k8s"}, Operator: ConditionEqual}},
		}
		_, err := all.ToPromMatchers(ctx, nil)
		assert.Error(t, err)
	})
	t.Run("encode_func", func(t *testing.T) {
		all := AllConditions{
			{{DimensionName: "scene", Value: []string{"log"}, Operator: ConditionEqual}},
		}
		enc := func(s string) string { return "PREFIX_" + s }
		ms, err := all.ToPromMatchers(ctx, enc)
		require.NoError(t, err)
		require.Len(t, ms, 1)
		assert.Equal(t, "PREFIX___bk_query_label_selector_scene", ms[0].Name)
	})
}

func TestConditionField_ContainsToPromReg(t *testing.T) {
	t.Run("empty_value_noop", func(t *testing.T) {
		c := ConditionField{DimensionName: "x", Value: nil, Operator: ConditionContains}
		got := c.ContainsToPromReg()
		require.Same(t, &c, got)
		assert.Equal(t, ConditionContains, c.Operator)
		assert.Nil(t, c.Value)
	})

	t.Run("single_contains_to_eq", func(t *testing.T) {
		c := ConditionField{Operator: ConditionContains, Value: []string{"log"}}
		c.ContainsToPromReg()
		assert.Equal(t, ConditionEqual, c.Operator)
		assert.Equal(t, []string{"log"}, c.Value)
	})

	t.Run("single_ncontains_to_ne", func(t *testing.T) {
		c := ConditionField{Operator: ConditionNotContains, Value: []string{"metric"}}
		c.ContainsToPromReg()
		assert.Equal(t, ConditionNotEqual, c.Operator)
		assert.Equal(t, []string{"metric"}, c.Value)
	})

	t.Run("single_eq_unchanged_operator", func(t *testing.T) {
		c := ConditionField{Operator: ConditionEqual, Value: []string{"v"}}
		c.ContainsToPromReg()
		assert.Equal(t, ConditionEqual, c.Operator)
		assert.Equal(t, []string{"v"}, c.Value)
	})

	t.Run("multi_eq_to_req_quoted_full_match", func(t *testing.T) {
		c := ConditionField{Operator: ConditionEqual, Value: []string{"a.b", "c"}}
		c.ContainsToPromReg()
		assert.Equal(t, ConditionRegEqual, c.Operator)
		require.Len(t, c.Value, 1)
		assert.Equal(t, `^(a\.b|c)$`, c.Value[0])
	})

	t.Run("multi_ne_to_nreq_quoted", func(t *testing.T) {
		c := ConditionField{Operator: ConditionNotEqual, Value: []string{"x", "y"}}
		c.ContainsToPromReg()
		assert.Equal(t, ConditionNotRegEqual, c.Operator)
		require.Len(t, c.Value, 1)
		assert.Equal(t, `^(x|y)$`, c.Value[0])
	})

	t.Run("multi_contains_to_req_quoted", func(t *testing.T) {
		c := ConditionField{Operator: ConditionContains, Value: []string{"p|q", "r"}}
		c.ContainsToPromReg()
		assert.Equal(t, ConditionRegEqual, c.Operator)
		require.Len(t, c.Value, 1)
		// | and meta in literals are escaped
		assert.Equal(t, `^(p\|q|r)$`, c.Value[0])
	})

	t.Run("multi_ncontains_to_nreq_quoted", func(t *testing.T) {
		c := ConditionField{Operator: ConditionNotContains, Value: []string{"a", "b"}}
		c.ContainsToPromReg()
		assert.Equal(t, ConditionNotRegEqual, c.Operator)
		require.Len(t, c.Value, 1)
		assert.Equal(t, `^(a|b)$`, c.Value[0])
	})

	t.Run("multi_req_passthrough_regex_no_wrapper", func(t *testing.T) {
		c := ConditionField{Operator: ConditionRegEqual, Value: []string{`foo`, `bar.*`}}
		c.ContainsToPromReg()
		assert.Equal(t, ConditionRegEqual, c.Operator)
		require.Len(t, c.Value, 1)
		assert.Equal(t, `foo|bar.*`, c.Value[0])
	})

	t.Run("multi_nreq_passthrough_regex_no_wrapper", func(t *testing.T) {
		c := ConditionField{Operator: ConditionNotRegEqual, Value: []string{`a`, `b`}}
		c.ContainsToPromReg()
		assert.Equal(t, ConditionNotRegEqual, c.Operator)
		require.Len(t, c.Value, 1)
		assert.Equal(t, `a|b`, c.Value[0])
	})
}
