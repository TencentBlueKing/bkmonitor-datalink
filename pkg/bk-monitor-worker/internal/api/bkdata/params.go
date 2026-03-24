// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package bkdata

import (
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
)

// AccessDeployPlanParams AccessDeployPlan的结构化参数
func AccessDeployPlanParams(rawDataName, rawDataAlias, master, group, topic, user, password string, tasks uint, useSasl bool) map[string]any {
	return map[string]any{
		"bk_app_code":   config.BkApiAppCode,
		"bk_username":   "admin",
		"data_scenario": "queue",
		"bk_biz_id":     config.BkdataBkBizId,
		"description":   "",
		"access_raw_data": map[string]any{
			"raw_data_name":    rawDataName,
			"maintainer":       config.BkdataProjectMaintainer,
			"raw_data_alias":   rawDataAlias,
			"data_source":      "kafka",
			"data_encoding":    "UTF-8",
			"sensitivity":      "private",
			"description":      fmt.Sprintf("接入配置 (%s)", rawDataAlias),
			"tags":             []any{},
			"data_source_tags": []string{"src_kafka"},
		},
		"access_conf_info": map[string]any{
			"collection_model": map[string]any{"collection_type": "incr", "start_at": 1, "period": "-1"},
			"resource": map[string]any{
				"type": "kafka",
				"scope": []map[string]any{
					{
						"master":            master,
						"group":             group,
						"topic":             topic,
						"tasks":             tasks,
						"use_sasl":          useSasl,
						"security_protocol": "SASL_PLAINTEXT",
						"sasl_mechanism":    "SCRAM-SHA-512",
						"user":              user,
						"password":          password,
					},
				},
			},
		},
	}
}

type DatabusCleansParams struct {
	RawDataId            int              `json:"raw_data_id"`
	JsonConfig           string           `json:"json_config"`
	PEConfig             string           `json:"pe_config"`
	BkBizId              int              `json:"bk_biz_id"`
	Description          string           `json:"description"`
	CleanConfigName      string           `json:"clean_config_name"`
	ResultTableName      string           `json:"result_table_name"`
	ResultTableNameAlias string           `json:"result_table_name_alias"`
	Fields               []map[string]any `json:"fields"`
	BkUsername           string           `json:"bk_username"`
}

type StopDatabusCleansParams struct {
	RawDataId            int              `json:"raw_data_id"`
	JsonConfig           string           `json:"json_config"`
	PEConfig             string           `json:"pe_config"`
	BkBizId              int              `json:"bk_biz_id"`
	Description          string           `json:"description"`
	CleanConfigName      string           `json:"clean_config_name"`
	ResultTableName      string           `json:"result_table_name"`
	ResultTableNameAlias string           `json:"result_table_name_alias"`
	Fields               []map[string]any `json:"fields"`
	BkUsername           string           `json:"bk_username"`
}

type DataFlowNodeParams struct {
	FromLinks    []map[string]any `json:"from_links"`
	NodeType     string           `json:"node_type"`
	Config       map[string]any   `json:"config"`
	FrontendInfo map[string]int   `json:"frontend_info"`
}

type UpdateDataFlowNodeParams struct {
	DataFlowNodeParams
	NodeId int `json:"node_id"`
}
