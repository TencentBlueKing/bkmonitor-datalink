// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package tsdb

// VmQueryCheckBody VictoriaMetrics 在 check 预览中的返回体。
type VmQueryCheckBody struct {
	StorageType string `json:"storage_type" example:"victoria_metrics"`
	// MetricQL 直查 VM 内存预览：ToPromExpr（空 PromExprOption）与 queryTsToInstanceAndStmt 的 stmt 骨架一致，再按 ToVmExpand.MetricFilterCondition 将引用名整词替换为 {...}。拼装响应时由 check 路径 metadata.SetCheckPreviewMetricQL 写入、GetRequestBody 内 GetCheckPreviewMetricQL 读出。
	MetricQL string `json:"metricql"`
	// ResultTableList ToVmExpand.ResultTableList，与正式直查 SetExpand 后下发 VM 的 result_table 列表同源。
	ResultTableList []string `json:"result_table_id"`
}
