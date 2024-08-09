// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package define

import (
	"fmt"
)

const (
	NameGatherUp    = "bkm_gather_up"
	LabelUpCode     = "bkm_up_code"
	LabelUpCodeName = "bkm_up_code_name"

	NameMetricBeatUp             = "bkm_metricbeat_endpoint_up"
	NameMetricBeatScrapeDuration = "bkm_metricbeat_scrape_duration_seconds"
	NameMetricBeatScrapeSize     = "bkm_metricbeat_scrape_size_bytes"
	NameMetricBeatScrapeLine     = "bkm_metricbeat_scrape_line"
)

func MetricBeatUp(code NamedCode) string {
	return fmt.Sprintf(`%s{code="%d"} 1`, NameMetricBeatUp, code.Code())
}

func MetricBeatScrapeDuration(seconds float64) string {
	return fmt.Sprintf(`%s{} %f`, NameMetricBeatScrapeDuration, seconds)
}

func MetricBeatScrapeSize(size int) string {
	return fmt.Sprintf(`%s{} %d`, NameMetricBeatScrapeSize, size)
}

func MetricBeatScrapeLine(n int) string {
	return fmt.Sprintf(`%s{} %d`, NameMetricBeatScrapeLine, n)
}

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
	CodeOK                        = newNamedCode(0, "成功")
	CodeUnknown                   = newNamedCode(1, "未知")
	CodeCancel                    = newNamedCode(2, "取消")
	CodeTimeout                   = newNamedCode(3, "超时")
	CodeInternalErr               = newNamedCode(4, "系统内部异常")
	CodeMetricBeatWriteFileErr    = newNamedCode(1501, "将响应同步至临时文件失败")
	CodeMetricBeatConnErr         = newNamedCode(2501, "连接用户端地址失败")
	CodeMetricBeatFormatErr       = newNamedCode(2502, "服务返回的 Prom 数据格式异常")
	CodeScriptRunErr              = newNamedCode(2301, "脚本运行报错")
	CodeScriptFormatErr           = newNamedCode(2302, "脚本打印的 Prom 数据格式异常")
	CodeScriptNoOutputErr         = newNamedCode(2303, "脚本没有输出内容")
	CodeScriptTimeoutErr          = newNamedCode(2304, "脚本执行超时")
	CodeNetConnErr                = newNamedCode(1000, "连接失败")
	CodeNetConnTimeoutErr         = newNamedCode(1001, "连接超时")
	CodeNetRequestErr             = newNamedCode(1100, "请求失败")
	CodeNetRequestTimeoutErr      = newNamedCode(1101, "请求超时")
	CodeNetRequestDeadLineErr     = newNamedCode(1102, "超时设置错误")
	CodeNetRequestInitErr         = newNamedCode(1103, "请求初始化失败")
	CodeNetResponseErr            = newNamedCode(1200, "响应失败")
	CodeNetResponseTimeoutErr     = newNamedCode(1201, "响应超时")
	CodeNetResponseMatchErr       = newNamedCode(1202, "匹配失败")
	CodeNetResponseNotFindIpv4Err = newNamedCode(1211, "未找到 IPV4 地址")
	CodeNetResponseNotFindIpv6Err = newNamedCode(1212, "未找到 IPV6 地址")
	CodeNetResponseParseUrlErr    = newNamedCode(1213, "解析 URL 错误")
	CodeDNSResolveErr             = newNamedCode(1004, "解析 DNS 失败")
	CodeNetResponseCodeErr        = newNamedCode(1203, "响应码不匹配")
	CodeNetResponseHandleErr      = newNamedCode(1206, "响应处理失败")
	CodeNetResponseConnRefusedErr = newNamedCode(1207, "链接拒绝")
	CodeNetInvalidIPErr           = newNamedCode(2102, "IP 格式异常")
)
