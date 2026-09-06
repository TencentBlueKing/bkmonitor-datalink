// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package datasources

import "linkd/internal/lifecycle/enrich"

const (
	SampleTenantID         = "tenant-1"
	SampleStrategyID int64 = 123
	SampleHistoryID  int64 = 70001
	SampleBizID      int64 = 2
)

// Mock 提供人工构造、按租户和查询身份严格匹配的 BASE_COLLECT 固定依赖数据。
type Mock struct{}

// Sources 返回共享相同固定样例的窄读取接口集合。
func (*Mock) Sources() enrich.Sources {
	return enrich.Sources{
		BKStrategy: &MockBKStrategyClient{},
		CWStrategy: &MockCWStrategyClient{},
	}
}
