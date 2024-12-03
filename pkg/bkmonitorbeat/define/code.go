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
	NameGatherUp    = "bkm_gather_up"
	LabelUpCode     = "bkm_up_code"
	LabelUpCodeName = "bkm_up_code_name"

	// MetricBeat 任务指标

	NameMetricBeatUp             = "bkm_metricbeat_endpoint_up"
	NameMetricBeatScrapeDuration = "bkm_metricbeat_scrape_duration_seconds"
	NameMetricBeatScrapeSize     = "bkm_metricbeat_scrape_size_bytes"
	NameMetricBeatScrapeLine     = "bkm_metricbeat_scrape_line"
	NameMetricBeatHandleDuration = "bkm_metricbeat_handle_duration_seconds"

	// KubeEvent 任务指标

	NameKubeEventReceiveEvents = "bkm_kubeevent_receive_events"
	NameKubeEventReportEvents  = "bkm_kubeevent_report_events"
	NameKubeEventCleanedEvents = "bkm_kubeevent_cleaned_events"
)

type NamedCode struct {
	code int
	name string
}

func (nc NamedCode) Code() int {
	return nc.code
}

func (nc NamedCode) Name() string {
	return nc.name
}

func newNamedCode(code int, name string) NamedCode {
	return NamedCode{code: code, name: name}
}

var (
	CodeOK       = newNamedCode(0, "Ok")
	CodeUnknown  = newNamedCode(1, "Unknown")
	CodeCanceled = newNamedCode(2, "Canceled")
	CodeTimeout  = newNamedCode(3, "Timeout")

	CodeWriteTempFileFailed = newNamedCode(1501, "WriteTempFileFailed")
	CodeConnTimeout         = newNamedCode(1001, "ConnTimeout")
	CodeConnFailed          = newNamedCode(1000, "ConnFailed")
	CodeConnRefused         = newNamedCode(2501, "ConnRefused")
	CodeInvalidPromFormat   = newNamedCode(2502, "InvalidPromFormat")
	CodeScriptRunFailed     = newNamedCode(2301, "ScriptRunFailed")
	CodeScriptNoOutput      = newNamedCode(2303, "ScriptNoOutput")
	CodeScriptTimeout       = newNamedCode(2304, "ScriptRunTimeout")
	CodeRequestFailed       = newNamedCode(1100, "RequestFailed")
	CodeRequestTimeout      = newNamedCode(1101, "RequestTimeout")
	CodeResponseFailed      = newNamedCode(1200, "ResponseFailed")
	CodeResponseNotMatch    = newNamedCode(1202, "ResponseNotMatch")
	CodeIPNotFound          = newNamedCode(1211, "IPNotFound")
	CodeInvalidURL          = newNamedCode(1213, "InvalidURL")
	CodeDNSResolveFailed    = newNamedCode(1004, "DNSResolveFailed")
	CodeInvalidIP           = newNamedCode(2102, "InvalidIP")
	CodeBadRequestParams    = newNamedCode(1103, "BadRequestParams")
)
