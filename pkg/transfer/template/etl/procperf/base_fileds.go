// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package procperf

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/etl"
)

// ProcBaseDimensionFieldsValue :
func ProcBaseDimensionFieldsValue() []etl.Field {
	return []etl.Field{
		etl.NewSimpleField(
			define.RecordIPFieldName,
			etl.ExtractByJMESPath("ip"), etl.TransformNilString,
		),
		etl.NewSimpleField(
			define.RecordTargetIPFieldName,
			etl.ExtractByJMESPath("ip"), etl.TransformNilString,
		),
		etl.NewSimpleField(
			define.RecordSupplierIDFieldName,
			etl.ExtractByJMESPath("bizid"), etl.TransformNilString,
		),
		etl.NewSimpleField(
			define.RecordCloudIDFieldName,
			etl.ExtractByJMESPath("cloudid"), etl.TransformNilString,
		),
		etl.NewSimpleField(
			define.RecordTargetCloudIDFieldName,
			etl.ExtractByJMESPath("cloudid"), etl.TransformNilString,
		),
		etl.NewSimpleField(
			define.RecordBKAgentID,
			etl.ExtractByJMESPath("bk_agent_id"), etl.TransformNilString,
		),
		etl.NewSimpleFieldWithCheck(
			define.RecordBKBizID,
			etl.ExtractByJMESPath("bk_biz_id"), etl.TransformNilString, func(v interface{}) bool {
				return !etl.IfEmptyStringField(v)
			},
		),
		etl.NewSimpleField(
			define.RecordBKHostID,
			etl.ExtractByJMESPath("bk_host_id"), etl.TransformNilString,
		),
		etl.NewSimpleField(
			define.RecordTargetHostIDFieldName,
			etl.ExtractByJMESPath("bk_host_id"), etl.TransformNilString,
		),
		etl.NewSimpleField(
			define.RecordHostNameFieldName,
			etl.ExtractByJMESMultiPath("bkmonitorbeat.hostname", "agent.hostname", "hostname"), etl.TransformNilString,
		),
	}
}
