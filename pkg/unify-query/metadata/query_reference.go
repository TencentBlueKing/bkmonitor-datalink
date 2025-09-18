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
	"encoding/json"
	"strings"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/prompb"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/set"
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

func (q *Query) DataReload(data map[string]any) {
	if data == nil {
		return
	}

	data[KeyTableID] = q.TableID
	data[KeyDataLabel] = q.DataLabel
	data[KeyTableUUID] = q.TableUUID()
}

// StorageUUID 获取存储唯一标识
// storageType 存储类型
// storageID 存储唯一标识
// storageName 集群名称
// measurementType 表类型
// timeField 内置时间配置
func (q *Query) StorageUUID() string {
	var l []string
	for _, s := range []any{
		q.StorageType, q.StorageID, q.StorageName, q.MeasurementType, q.TimeField,
	} {
		switch ns := s.(type) {
		case string:
			if ns != "" {
				l = append(l, ns)
			}
		default:
			nt, _ := json.Marshal(ns)
			if len(nt) > 0 {
				l = append(l, string(nt))
			}
		}
	}

	return strings.Join(l, "|")
}

// TableUUID 查询主体 tableID + storageID + sliceID 作为查询主体的唯一标识
func (q *Query) TableUUID() string {
	var l []string
	for _, s := range []string{
		q.TableID, q.StorageID, q.SliceID,
	} {
		if s != "" {
			l = append(l, s)
		}
	}

	return strings.Join(l, "|")
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
