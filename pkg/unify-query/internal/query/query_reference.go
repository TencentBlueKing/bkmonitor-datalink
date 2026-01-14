// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package query

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/set"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

func ToVmExpand(ctx context.Context, qr metadata.QueryReference) (*metadata.VmExpand, error) {
	vmClusterNames := set.New[string]()
	vmResultTable := set.New[string]()
	metricFilterCondition := make(map[string]string)

	for referenceName, references := range qr {
		if len(references) == 0 {
			continue
		}

		// 因为是直查，reference 还需要承担聚合语法生成，所以 vm 不支持同指标的拼接，所以这里只取第一个 reference
		reference := references[0]
		if 0 < len(reference.QueryList) {
			vmConditions := set.New[string]()
			for _, query := range reference.QueryList {
				if query.VmRt == "" {
					continue
				}

				vmResultTable.Add(query.VmRt)
				vmConditions.Add(string(query.VmCondition))
				vmClusterNames.Add(query.StorageName)
			}

			filterCondition := ""
			if vmConditions.Size() > 0 {
				filterCondition = fmt.Sprintf(`%s`, strings.Join(vmConditions.ToArray(), ` or `))
			}

			metricFilterCondition[referenceName] = filterCondition
		}
	}

	influxdbRouter := influxdb.GetInfluxDBRouter() // 获取InfluxDB Router实例

	// 黑名单信息已通过 influxdb.Service 的订阅机制自动更新，直接使用内存中已缓存的黑名单信息进行检查
	isConflict := influxdbRouter.IsCheckVmBlock(ctx, vmClusterNames)

	if isConflict {
		return nil, fmt.Errorf("vm Cluster conflict")
	}

	if vmResultTable.Size() == 0 {
		return nil, nil
	}

	vmExpand := &metadata.VmExpand{
		MetricFilterCondition: metricFilterCondition,
		ResultTableList:       vmResultTable.ToArray(),
	}
	sort.Strings(vmExpand.ResultTableList)

	// 当所有的 vm 集群都一样的时候，才进行传递
	if vmClusterNames.Size() == 1 {
		vmExpand.ClusterName = vmClusterNames.First()
	}

	return vmExpand, nil
}
