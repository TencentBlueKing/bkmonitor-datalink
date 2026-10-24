// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package api

import "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"

// selectLegacyInstantPath 保持旧接口的首条有效路径语义。
// 当所有路径都没有数据时，返回最后一条成功执行的路径，兼容原有行为。
func selectLegacyInstantPath(paths []cmdb.RelationMultiResourcePathData) ([]string, cmdb.Matchers) {
	var (
		path       []string
		targetList cmdb.Matchers
	)
	for _, item := range paths {
		path = item.Path
		targetList = item.TargetList
		if len(item.TargetList) > 0 {
			return path, targetList
		}
	}
	return path, targetList
}

// selectLegacyRangePath 保持旧接口的首条有效路径语义。
func selectLegacyRangePath(paths []cmdb.RelationMultiResourceRangePathData) ([]string, []cmdb.MatchersWithTimestamp) {
	var (
		path       []string
		targetList []cmdb.MatchersWithTimestamp
	)
	for _, item := range paths {
		path = item.Path
		targetList = item.TargetList
		if len(item.TargetList) > 0 {
			return path, targetList
		}
	}
	return path, targetList
}
