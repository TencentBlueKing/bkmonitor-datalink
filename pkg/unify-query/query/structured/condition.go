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
	"fmt"
	"strconv"
	"strings"

	"github.com/pkg/errors"
	"github.com/prometheus/prometheus/model/labels"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
)

const (
	ConditionOr  = "or"
	ConditionAnd = "and"

	Contains  = "contains"
	Ncontains = "ncontains"
)

// 过滤条件，包含了字段描述以及条件的组合方式
type Conditions struct {
	// FieldList 查询条件
	FieldList []ConditionField `json:"field_list,omitempty"`
	// ConditionList 组合条件，长度 = len(FieldList) - 1的数组，支持 and,or
	ConditionList []string `json:"condition_list,omitempty" example:"and"`
}

// AnalysisConditions
func (c *Conditions) AnalysisConditions() ([][]ConditionField, error) {

	var (
		totalBuffer = make([][]ConditionField, 0) // 以or作为分界线，and条件的内容都会放入到一起，然后一起渲染处理
		rowBuffer   = make([]ConditionField, 0)   // 每一组的缓存
	)

	// 如果长度为0，直接返回
	if len(c.FieldList) == 0 {
		log.Debugf(context.TODO(), "no conditionField, will return empty one.")
		return nil, nil
	}

	if len(c.FieldList)-1 != len(c.ConditionList) {
		log.Debugf(context.TODO(),
			"field list->[%d] and condition list length->[%d] not match, nothing will affected, return error.",
			len(c.FieldList), len(c.ConditionList),
		)
		return nil, ErrFieldAndConditionListNotMatch
	}

	// 先循环遍历所有的内容，加入到各个列表中
	for index, field := range c.FieldList {
		// 不允许值为空，此时可能引起拼接失败
		if len(field.Value) == 0 {
			log.Warnf(context.TODO(), "missing value in condition:%s", field.DimensionName)
			return nil, errors.Wrap(ErrMissingValue, field.DimensionName)
		}
		// 第一组的只需要增加即可
		if index == 0 {
			log.Debugf(context.TODO(), "first element->[%s] will add to row buffer", field.String())
			rowBuffer = append(rowBuffer, field)
			continue
		}

		// 第二组的需要先判断条件是否or
		if c.ConditionList[index-1] == ConditionAnd {
			log.Debugf(context.TODO(), "under and condition, element->[%s] will continue add to row buffer", field)
			rowBuffer = append(rowBuffer, field)
		} else if c.ConditionList[index-1] == ConditionOr {
			log.Debugf(context.TODO(), "under or condition, will add element->[%s] to new row.", field)
			// 先追加到结果中
			totalBuffer = append(totalBuffer, rowBuffer)
			// 然后创建一个新的行数组放置新的内容
			rowBuffer = []ConditionField{field}
		} else {
			log.Errorf(context.TODO(), "unknown condition->[%s] in condition list, nothing will do.", c.ConditionList[index-1])
			return nil, ErrUnknownConditionOperator
		}
	}
	// 最后结束的时候，需要将所有的缓存放置到结果中
	log.Debugf(context.TODO(), "loop finish, will flush all row->[%d] to the buffer now", len(rowBuffer))
	totalBuffer = append(totalBuffer, rowBuffer)
	log.Debugf(context.TODO(), "total row->[%d] is found.", len(totalBuffer))

	return totalBuffer, nil
}

// ToProm
func (c *Conditions) ToProm() ([]*labels.Matcher, [][]ConditionField, error) {

	var (
		err         error
		totalBuffer [][]ConditionField // 以or作为分界线，and条件的内容都会放入到一起，然后一起渲染处理
		label       *labels.Matcher
		labelList   []*labels.Matcher
	)

	// 查询语法转化为 promql
	for i, cond := range c.FieldList {
		c.FieldList[i] = *(cond.ContainsToPromReg())
	}

	// 1. 判断请求是否为空，如果为空，则直接返回空的内容
	if len(c.FieldList) == 0 {
		log.Debugf(context.TODO(), "field list is empty, nothing will return .")
		return nil, nil, nil
	}

	if totalBuffer, err = c.AnalysisConditions(); err != nil {
		log.Errorf(context.TODO(), "failed to analysis conditions for->[%s], nothing will return.", err)
		return nil, nil, err
	}

	if totalBuffer == nil {
		log.Debugf(context.TODO(), "not condition need to return")
		return nil, nil, nil
	}

	// 2. 判断是否二维数组，如果是表示过滤存在or关系，需要往ctx中塞入新的过滤条件信息
	if len(totalBuffer) >= 2 {
		log.Debugf(context.TODO(),
			"condition is more than two level with->[%d], it contains or conditions, will update context.",
			len(totalBuffer),
		)
		return nil, totalBuffer, fmt.Errorf("or 过滤条件无法直接转换为 promql 语句，请使用结构化查询")
	}

	// 3. 如果是一维数组，表示是and关系，可以直接通过prom表达式进行表示
	for _, c := range totalBuffer[0] {
		// 如果发现有任何一个条件是存在contains，那么将这个buffer内容放置在ctx中返回
		// 这样做的原因是为了提高influxdb的实际查询效率，如果是使用正则的方式进行查询，效率会严重减低
		if c.Operator == ConditionContains || c.Operator == ConditionNotContains {
			log.Debugf(context.TODO(), "found op->[%s] which cause contains op, will return the whole buffer in ctx.", c.Operator)

			return nil, totalBuffer, nil
		}

		// 否则，就先构建对应的labelMatcher信息
		if label, err = labels.NewMatcher(c.ToPromOperator(), c.DimensionName, c.Value[0]); err != nil {
			log.Errorf(context.TODO(), "failed to make matcher for->[%s], will return err", err)
			return nil, nil, err
		}

		labelList = append(labelList, label)
	}

	return labelList, nil, nil
}

type AllConditions [][]ConditionField

func MergeConditionField(source, target AllConditions) AllConditions {
	if len(target) == 0 {
		return source
	}
	if len(source) == 0 {
		return target
	}

	all := make(AllConditions, 0, len(source)*len(target))

	for _, s := range target {
		for _, t := range source {
			cond := make([]ConditionField, 0, len(s)+len(t))
			cond = append(cond, s...)
			cond = append(cond, t...)
			all = append(all, cond)
		}
	}
	return all
}

func (c AllConditions) VMString(vMResultTable string) (string, int) {
	vmLabels := make([]string, 0, len(c))
	num := 0
	for _, cond := range c {
		labels := make([]string, 0, len(cond)+1)
		if vMResultTable != "" {
			num++
			labels = append(labels, fmt.Sprintf(`result_table_id="%s"`, vMResultTable))
		}

		for _, f := range cond {
			nf := f.ContainsToPromReg()
			val := strings.ReplaceAll(nf.Value[0], `\`, `\\`)
			labels = append(labels, fmt.Sprintf(`%s%s"%s"`, nf.DimensionName, nf.ToPromOperator(), val))
		}

		num += len(cond)
		vmLabels = append(vmLabels, strings.Join(labels, `, `))
	}

	return strings.Join(vmLabels, ` or `), num
}

// ConvertToPromBuffer
func ConvertToPromBuffer(totalBuffer [][]ConditionField) [][]promql.ConditionField {
	var promBuffer [][]promql.ConditionField
	promBuffer = make([][]promql.ConditionField, 0, len(totalBuffer))
	for _, buf := range totalBuffer {
		var fieldList []promql.ConditionField
		fieldList = make([]promql.ConditionField, 0, len(buf))
		for _, item := range buf {
			// contain和notcontiain，对应将operator转为eq和neq就行了,实际的信息以value为准即可
			if item.Operator == Contains {
				item.Operator = "eq"
			}
			if item.Operator == Ncontains {
				item.Operator = "ne"
			}
			fieldList = append(
				fieldList,
				promql.ConditionField{
					DimensionName: item.DimensionName, Value: item.Value, Operator: item.ToPromOperator().String(),
				},
			)
		}
		promBuffer = append(promBuffer, fieldList)
	}
	return promBuffer
}

// GetRequiredFiled: 从conditions中过滤出bk_biz_id, bcs_cluster, project_id
// bk_biz_id : 只支持eq和contains
// bcs_cluster_id : 支持eq contains ne reg nreg ncontains (不过建议还是能用eq和contains就用这俩)
// project_id : 只支持eq和contains
// todo: 这里bcs_cluster后续考虑在unify-query做全部的方法过滤
func (c *Conditions) GetRequiredFiled() ([]int, []string, []string, error) {
	var (
		bizIDs     []int
		projectIDs []string
		clusterIDs []string
	)

	for _, field := range c.FieldList {

		// 查询参数长度为0，忽略
		if len(field.Value) == 0 {
			continue
		}
		switch field.DimensionName {
		case BizID:
			if field.Operator != ConditionEqual && field.Operator != ConditionContains {
				return bizIDs, projectIDs, clusterIDs, fmt.Errorf("unsupport operations to filter %s, "+
					"only support %s, %s", BizID, ConditionEqual, ConditionContains)
			}
			for _, idStr := range field.Value {
				id, err := strconv.Atoi(idStr)
				if err != nil {
					return bizIDs, projectIDs, clusterIDs, errors.Wrap(err, "get required field")
				}
				bizIDs = append(bizIDs, id)
			}
		case ProjectID:
			if field.Operator != ConditionEqual && field.Operator != ConditionContains {
				return bizIDs, projectIDs, clusterIDs, fmt.Errorf("unsupport operations to filter %s, "+
					"only support %s, %s", ProjectID, ConditionEqual, ConditionContains)
			}
			projectIDs = append(projectIDs, field.Value...)
		case ClusterID:
			// bcs_cluster只在unify-query计算equal和contains 其他的类型暂时直接交给底层查询自己计算
			switch field.Operator {
			case ConditionEqual, ConditionContains:
				clusterIDs = append(clusterIDs, field.Value...)
			}
		}
	}

	return bizIDs, projectIDs, clusterIDs, nil
}

// ReplaceOrAddCondition: 替换或添加条件
func ReplaceOrAddCondition(c *Conditions, dimension string, values []string) *Conditions {

	// 无效的替换或添加
	if len(values) == 0 {
		return c
	}

	var (
		hasDimension = false
		allowOpMap   = map[string]struct{}{
			ConditionContains: {},
			ConditionEqual:    {},
		}
		op string
	)

	if len(values) == 1 {
		op = ConditionEqual
	} else {
		op = ConditionContains
	}

	for i, f := range c.FieldList {

		if f.DimensionName != dimension {
			continue
		}

		if _, has := allowOpMap[f.Operator]; !has {
			continue
		}
		hasDimension = true

		c.FieldList[i].Value = values
		c.FieldList[i].Operator = op
	}

	if hasDimension {
		return c
	}

	// 如果未替换bizID，则添加bizID中的值到条件中
	c.FieldList = append(c.FieldList, ConditionField{
		DimensionName: dimension,
		Value:         values,
		Operator:      op,
	})
	if len(c.FieldList) != 1 {
		c.ConditionList = append(c.ConditionList, ConditionAnd)
	}

	return c
}
