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
func AccessDeployPlanParams(rawDataName, rawDataAlias, master, group, topic, user, password string, tasks uint, useSasl bool) map[string]interface{} {
	return map[string]interface{}{
		"bk_app_code":   config.BkApiAppCode,
		"bk_username":   "admin",
		"data_scenario": "queue",
		"bk_biz_id":     config.GlobalBkdataBkBizId,
		"description":   "",
		"access_raw_data": map[string]interface{}{
			"raw_data_name":    rawDataName,
			"maintainer":       config.GlobalBkdataProjectMaintainer,
			"raw_data_alias":   rawDataAlias,
			"data_source":      "kafka",
			"data_encoding":    "UTF-8",
			"sensitivity":      "private",
			"description":      fmt.Sprintf("接入配置 (%s)", rawDataAlias),
			"tags":             []interface{}{},
			"data_source_tags": []string{"src_kafka"},
		},
		"access_conf_info": map[string]interface{}{
			"collection_model": map[string]interface{}{"collection_type": "incr", "start_at": 1, "period": "-1"},
			"resource": map[string]interface{}{
				"type": "kafka",
				"scope": []map[string]interface{}{
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
