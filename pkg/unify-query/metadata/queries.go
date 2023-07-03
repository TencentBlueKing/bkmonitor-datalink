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
	"fmt"
	"strings"

	"github.com/prometheus/prometheus/model/labels"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

// SetQueries 写入查询扩展，因为有多个查询所以除了常量前缀之外，还需要指定 k（该值一般为指标名，用于对应）
func SetQueries(ctx context.Context, queries *Queries) error {
	queries.ctx = ctx
	md.set(ctx, QueriesKey, queries)
	return nil
}

// GetQueries 如果查询异常，则直接报错
func GetQueries(ctx context.Context) *Queries {
	r, ok := md.get(ctx, QueriesKey)
	if ok {
		if v, ok := r.(*Queries); ok {
			return v
		}
	}
	return nil
}

// IsDirectly 判断是否是直查类型，返回是否是直查，以及报错信息
func (q *Queries) IsDirectly() (bool, error) {
	for _, queries := range q.Query {
		for _, query := range queries.QueryList {
			// 判断是否是直查类型，直插类型的指标名有特殊规则
			for _, t := range []string{consul.VictoriaMetricsStorageType} {
				if t == query.SourceType {
					err := q.checkDirectlyQuery()
					return err == nil, err
				}
			}
		}
	}

	return false, nil
}

// String 输出字符串
func (q *Queries) String() string {
	res, _ := json.Marshal(q)
	return string(res)
}

// DirectlyClusterID 获取直查的集群ID
func (q *Queries) DirectlyClusterID() string {
	return q.directlyClusterID
}

// DirectlyMetricName 获取直查的 metricName
func (q *Queries) DirectlyMetricName() map[string]string {
	return q.directlyMetricName
}

func (q *Queries) DirectlyResultTable() map[string][]string {
	return q.directlyResultTable
}

// DirectlyLabelsMatcher 获取额外的查询条件
func (q *Queries) DirectlyLabelsMatcher() map[string][]*labels.Matcher {
	return q.directlyLabelsMatcher
}

// GetIsCountReferenceNameList 获取用了 count 转换的 referenceName 列表
func (q *Queries) GetIsCountReferenceNameList() []string {
	var referenceNameList []string
	for _, queries := range q.Query {
		if queries.IsCount {
			referenceNameList = append(referenceNameList, queries.ReferenceName)
		}
	}
	return referenceNameList
}

// checkDirectlyQuery 判断是否是直查，目前 VM 是直查的方式
func (q *Queries) checkDirectlyQuery() error {
	var (
		tmpClusterID          = make(map[string]struct{})
		directlyMetricName    = make(map[string]string)
		directlyResultTable   = make(map[string][]string)
		directlyLabelsMatcher = make(map[string][]*labels.Matcher)
		err                   error
	)

	for referenceName, queries := range q.Query {
		// 不允许多 tableID 混查
		if len(queries.QueryList) > 1 {
			errorTableIds := make([]string, len(queries.QueryList))
			for i, qry := range queries.QueryList {
				errorTableIds[i] = fmt.Sprintf("%s,%s,%s,%s", qry.ClusterID, qry.DB, qry.Measurement, qry.Field)
			}
			err := fmt.Errorf("directly query is not support many table id : %+v", strings.Join(errorTableIds, "|"))
			return err
		}

		var (
			metricName string
			vmRts      = make(map[string]struct{})
		)

		for _, query := range queries.QueryList {
			// 不支持 or 条件查询
			if query.IsHasOr {
				return fmt.Errorf("directly query is not support conditions with or: %s", query.Condition)
			}

			// 获取空间自带过滤条件，上面已经判断不能用 or
			if len(query.Filters) > 0 {
				if len(query.Filters) > 1 {
					log.Errorf(q.ctx, "%+v", queries)
					return fmt.Errorf("directly query is not support filters with or: %+v", query.Filters)
				}
				directlyLabelsMatcher[referenceName] = make([]*labels.Matcher, len(query.Filters[0]))
				fi := 0
				for k, v := range query.Filters[0] {
					matcher, err := labels.NewMatcher(labels.MatchEqual, k, v)
					if err != nil {
						return err
					}
					directlyLabelsMatcher[referenceName][fi] = matcher
					fi++
				}
			}

			// 获取 vm 的指标名
			metricName = fmt.Sprintf("%s_%s", query.Measurement, query.Field)

			// 该查询下的所有 ClusterID 去重加入到 tmpClusterID
			if query.ClusterID != "" {
				tmpClusterID[query.ClusterID] = struct{}{}
			}
			if query.VmRt != "" {
				vmRts[query.VmRt] = struct{}{}
			}
		}

		directlyMetricName[referenceName] = metricName
		if len(vmRts) == 0 {
			err = fmt.Errorf("vm query result table is empty %s", metricName)
			break
		}
		directlyResultTable[metricName] = make([]string, 0, len(vmRts))
		for k := range vmRts {
			directlyResultTable[metricName] = append(directlyResultTable[metricName], k)
		}
	}

	// 如果是直查类型，则只支持单实例的方式，否则报错
	if len(tmpClusterID) != 1 {
		err = fmt.Errorf("directly query is not support many cluster id: %+v", tmpClusterID)
	}

	// 获取第一个
	for clusterID := range tmpClusterID {
		q.directlyClusterID = clusterID
		break
	}

	// 直查场景需要用到，指标以及空间自带的过滤条件
	q.directlyMetricName = directlyMetricName
	q.directlyResultTable = directlyResultTable
	q.directlyLabelsMatcher = directlyLabelsMatcher
	return err
}
