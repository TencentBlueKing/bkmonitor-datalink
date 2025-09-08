// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package define

const (
	SubConfigFieldDefault  = "default"
	SubConfigFieldService  = "service"
	SubConfigFieldInstance = "instance"
)

const (
	ConfigTypePrivileged = "privileged"
	ConfigTypePlatform   = "platform"
	ConfigTypeSubConfig  = "subconfig"
	ConfigTypeReportV2   = "report_v2"
	ConfigTypeReportV1   = "report"

	ConfigFieldApmConfig  = "apm"
	ConfigFieldProcessor  = "processor"
	ConfigFieldPipeline   = "pipeline"
	ConfigFieldReceiver   = "receiver"
	ConfigFieldPusher     = "bk_metrics_pusher"
	ConfigFieldExporter   = "exporter"
	ConfigFieldProxy      = "proxy"
	ConfigFieldPingserver = "pingserver"
)

type ApmConfig struct {
	Patterns []string `config:"patterns"`
}
