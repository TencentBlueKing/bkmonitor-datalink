// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package tsdb

import "errors"

// QueryCheckPreview check 等场景下按存储返回可 JSON 化的预览体；不得发起网络请求或修改全局状态。
type QueryCheckPreview interface {
	GetRequestBody() (any, error)
}

// VmQueryCheckBody VictoriaMetrics 在 check 预览中的返回体。
type VmQueryCheckBody struct {
	StorageType string `json:"storage_type" example:"victoria_metrics"`
	// MetricQL 直查 VM 内存预览：ToPromExpr（空 PromExprOption）与 queryTsToInstanceAndStmt 的 stmt 骨架一致，再按 ToVmExpand.MetricFilterCondition 将引用名整词替换为 {...}
	MetricQL string `json:"metricql"`
	// ResultTableList ToVmExpand.ResultTableList，与正式直查 SetExpand 后下发 VM 的 result_table 列表同源。
	ResultTableList []string `json:"result_table_id"`
}

func (v *VmQueryCheckBody) GetRequestBody() (any, error) {
	if v == nil {
		return nil, errors.New("nil VmQueryCheckBody")
	}
	return v, nil
}

// DorisQueryCheckBody Doris 存储在 check 预览中的返回体
type DorisQueryCheckBody struct {
	//todo
}

func (d *DorisQueryCheckBody) GetRequestBody() (any, error) {
	if d == nil {
		return nil, errors.New("nil DorisQueryCheckBody")
	}
	return d, nil
}

// ElasticsearchQueryCheckBody Elasticsearch 在 check 预览中的返回体
type ElasticsearchQueryCheckBody struct {
	//todo
}

func (e *ElasticsearchQueryCheckBody) GetRequestBody() (any, error) {
	if e == nil {
		return nil, errors.New("nil ElasticsearchQueryCheckBody")
	}
	return e, nil
}
