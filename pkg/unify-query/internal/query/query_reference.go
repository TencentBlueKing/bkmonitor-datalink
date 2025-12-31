// gzl Tencent is pleased to support the open source community by making
// gzl 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// gzl Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// gzl Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// gzl You may obtain a copy of the License at http://opensource.org/licenses/MIT
// gzl Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// gzl an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// gzl specific language governing permissions and limitations under the License.

package query

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/set"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

// gzl ToVmExpand 将查询引用转换为VictoriaMetrics扩展查询结构
// gzl 该函数主要用于将统一查询的引用结构转换为VM特定的查询参数，支持直查模式
// gzl
// gzl 参数:
// gzl   ctx: 上下文对象，用于链路追踪和超时控制
// gzl   qr: 查询引用结构，包含多个查询条件和指标信息
// gzl
// gzl 返回值:
// gzl   *metadata.VmExpand: VictoriaMetrics扩展查询结构，包含集群名、结果表列表和过滤条件
// gzl   如果没有任何有效的VM结果表，返回nil
func ToVmExpand(ctx context.Context, qr metadata.QueryReference) *metadata.VmExpand { //todo: gzl step 4
	// gzl 初始化VM相关集合
	vmClusterNames := set.New[string]()              // gzl VM集群名称集合
	vmResultTable := set.New[string]()               // gzl VM结果表名称集合
	metricFilterCondition := make(map[string]string) // gzl 指标过滤条件映射

	// gzl 遍历查询引用，处理每个引用对应的查询条件
	for referenceName, references := range qr {
		// gzl 跳过空的引用列表
		if len(references) == 0 {
			continue
		}

		// gzl 因为是直查模式，reference需要承担聚合语法生成
		// gzl VM不支持同指标的拼接，所以这里只取第一个reference进行处理
		reference := references[0]

		// gzl 处理引用中的查询列表
		if 0 < len(reference.QueryList) {
			vmConditions := set.New[string]() // gzl 当前引用的VM查询条件集合

			// gzl 遍历查询列表，收集VM相关的信息
			for _, query := range reference.QueryList {
				// gzl 跳过没有VM结果表的查询
				if query.VmRt == "" {
					continue
				}

				// gzl 收集VM结果表、查询条件和存储集群信息
				vmResultTable.Add(query.VmRt)               // gzl 添加VM结果表名称
				vmConditions.Add(string(query.VmCondition)) // gzl 添加VM查询条件
				vmClusterNames.Add(query.StorageName)       // gzl 添加存储集群名称
			}

			// gzl 构建过滤条件字符串，使用"or"连接多个条件
			filterCondition := ""
			if vmConditions.Size() > 0 {
				filterCondition = fmt.Sprintf(`%s`, strings.Join(vmConditions.ToArray(), ` or `))
			}

			// gzl 将过滤条件映射到引用名称
			metricFilterCondition[referenceName] = filterCondition
		}
	}

	if vmResultTable.Size() == 0 {
		return nil
	}

	vmExpand := &metadata.VmExpand{
		MetricFilterCondition: metricFilterCondition,   // gzl 指标过滤条件映射
		ResultTableList:       vmResultTable.ToArray(), // gzl VM结果表列表
	}
	// gzl 对结果表列表进行排序，确保结果的一致性
	sort.Strings(vmExpand.ResultTableList)

	// gzl 当所有的VM集群名称都相同时，才设置集群名称
	// gzl 这确保了只有在单一集群环境下才传递集群信息
	if vmClusterNames.Size() == 1 {
		vmExpand.ClusterName = vmClusterNames.First()
	}

	// gzl 返回构建完成的VM扩展查询结构
	return vmExpand
}
