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
	"context"
	"strings"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/prompb"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/set"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

func SetQueryReference(ctx context.Context, reference QueryReference) {
	md.set(ctx, QueryReferenceKey, reference)
	return
}

func GetQueryReference(ctx context.Context) QueryReference {
	r, ok := md.get(ctx, QueryReferenceKey)
	if ok {
		if v, ok := r.(QueryReference); ok {
			return v
		}
	}
	return nil
}

// ConfigureAlias 根据别名把 query 里面涉及到的字段都转换成别名查询
func (q *Query) ConfigureAlias(ctx context.Context) {
	if len(q.FieldAlias) == 0 {
		return
	}

	var (
		err error
	)
	ctx, span := trace.NewSpan(ctx, "configure-alias")
	defer span.End(&err)

	span.Set("field-alias", q.FieldAlias)

	// 替换 Field
	q.Field = q.FieldAlias.Alias(q.Field)

	// 替换维度
	for aggIdx, agg := range q.Aggregates {
		q.Aggregates[aggIdx].Field = q.FieldAlias.Alias(agg.Field)
		for dimIdx, dim := range agg.Dimensions {
			q.Aggregates[aggIdx].Dimensions[dimIdx] = q.FieldAlias.Alias(dim)
		}
	}

	// 替换过滤条件
	for conIdx, con := range q.AllConditions {
		for dimIdx, dim := range con {
			q.AllConditions[conIdx][dimIdx].DimensionName = q.FieldAlias.Alias(dim.DimensionName)
		}
	}

	// 替换保留字段
	for idx, s := range q.Source {
		q.Source[idx] = q.FieldAlias.Alias(s)
	}

	// 替换排序字段
	for idx, o := range q.Orders {
		q.Orders[idx].Name = q.FieldAlias.Alias(o.Name)
	}

	// 替换折叠字段
	if q.Collapse != nil {
		q.Collapse.Field = q.FieldAlias.Alias(q.Collapse.Field)
	}

	qStr, _ := json.Marshal(q)
	span.Set("query-json", string(qStr))
}

// TableUUID 查询主体 tableID + storageID + sliceID 作为查询主体的唯一标识
func (q *Query) TableUUID() string {
	return strings.Join([]string{
		q.TableID, q.StorageID, q.SliceID,
	}, "|")
}

// MetricLabels 获取真实指标名称
func (q *Query) MetricLabels(ctx context.Context) *prompb.Label {
	if GetQueryParams(ctx).IsReference {
		return nil
	}

	var (
		metrics    []string
		encodeFunc = GetFieldFormat(ctx).EncodeFunc()
	)

	if q.DataSource != "" {
		metrics = append(metrics, q.DataSource)
	}

	for _, n := range strings.Split(q.TableID, ".") {
		metrics = append(metrics, n)
	}
	metrics = append(metrics, q.Field)

	metricName := strings.Join(metrics, ":")
	if encodeFunc != nil {
		metricName = encodeFunc(metricName)
	}

	return &prompb.Label{
		Name:  labels.MetricName,
		Value: metricName,
	}
}

// CheckDruidQuery 判断是否是 druid 查询
func (q *Query) CheckDruidQuery(ctx context.Context, dims *set.Set[string]) bool {
	checkDims := set.New[string]([]string{"bk_obj_id", "bk_inst_id"}...)

	// 判断查询条件中是否有以上两个维度中的任意一个
	isDruid := func() bool {
		for _, conditions := range q.AllConditions {
			for _, con := range conditions {
				if checkDims.Existed(con.DimensionName) {
					return true
				}
			}
		}

		if dims.Intersection(checkDims).Size() > 0 {
			return true
		}

		return false
	}()

	// 如果是查询 druid 的数据，vt 名称需要进行替换
	if isDruid {

		replaceLabels := make(ReplaceLabels)

		// 替换 vmrt 的值
		oldVmRT := q.VmRt
		var newVmRT string
		if q.CmdbLevelVmRt != "" {
			newVmRT = q.CmdbLevelVmRt
		} else {
			newVmRT = strings.TrimSuffix(oldVmRT, MaDruidQueryRawSuffix) + MaDruidQueryCmdbSuffix
		}

		if newVmRT != oldVmRT {
			q.VmRt = newVmRT

			replaceLabels["result_table_id"] = ReplaceLabel{
				Source: oldVmRT,
				Target: newVmRT,
			}
		}

		q.VmCondition = ReplaceVmCondition(q.VmCondition, replaceLabels)
	}
	return isDruid
}
