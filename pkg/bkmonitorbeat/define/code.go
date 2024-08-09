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
	NameMetricBeatUp             = "bkm_metricbeat_endpoint_up"
	NameMetricBeatScrapeDuration = "bkm_metricbeat_scrape_duration_seconds"
	NameMetricBeatScrapeSize     = "bkm_metricbeat_scrape_size_bytes"
	NameMetricBeatScrapeLine     = "bkm_metricbeat_scrape_line"
)

func MetricBeatUp(code int) string {
	return fmt.Sprintf(`%s{code="%d"} 1`, NameMetricBeatUp, code)
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
	CodeMetricBeatWriteFileErr = newNamedCode(1501, "将响应同步至临时文件失败")
	CodeMetricBeatConnErr      = newNamedCode(2501, "连接用户端地址失败")
	CodeMetricBeatFormatErr    = newNamedCode(2502, "服务返回的 Prom 数据格式异常")
)

// BeatErrorCode : beat task error code
type BeatErrorCode int

// BeatErrorCodes
const (
	BeatErrCodeOK      = 0 // 成功
	BeatErrCodeUnknown = 1 // 未知
	BeatErrCodeCancel  = 2 // 取消
	BeatErrCodeTimeout = 3 // 超时
	BeatErrInternalErr = 4 // 系统内部异常

	BeatErrCodeConnError           = 1000 // 连接失败
	BeatErrCodeConnTimeoutError    = 1001 // 连接超时
	BeatErrCodeConnProxyError      = 1002 // 连接代理失败
	BeatErrCodeConnDNSResolveError = 1003 // 连接DNS解析失败
	BeatErrCodeDNSResolveError     = 1004 // DNS解析失败
	BeatInvalidIPError             = 1005 // 非法IP地址

	BeatErrCodeRequestError         = 1100 // 请求失败
	BeatErrCodeRequestTimeoutError  = 1101 // 请求超时
	BeatErrCodeRequestDeadLineError = 1102 // 超时设置错误
	BeatErrCodeRequestInitError     = 1103 // 请求初始化失败

	BeatErrCodeResponseError          = 1200 // 响应失败
	BeatErrCodeResponseTimeoutError   = 1201 // 响应超时
	BeatErrCodeResponseMatchError     = 1202 // 匹配失败
	BeatErrCodeResponseCodeError      = 1203 // 响应码不匹配
	BeatErrCodeResponseTemporaryError = 1204 // 临时响应失败
	BeatErrCodeResponseNoRspError     = 1205 // 服务无响应
	BeatErrCodeResponseHandleError    = 1206 // 响应处理失败
	BeatErrCodeResponseConnRefused    = 1207 // 链接拒绝
	BeatErrCodeResponseReadError      = 1208 // 响应读取失败
	BeatErrCodeResponseEmptyError     = 1209 // 响应头部为空
	BeatErrCodeResponseHeaderError    = 1210 // 响应头部不符合
	BeatErrCodeResponseNotFindIpv4    = 1211 // 未找到ipv4地址
	BeatErrCodeResponseNotFindIpv6    = 1212 // 未找到ipv6地址
	BeatErrCodeResponseParseUrlErr    = 1213 // url解析错误

	BeatErrScriptTsUnitConfigError = 1301 // 脚本配置中的时间单位设置异常

	BeaterProcSnapshotReadError  = 1402 // 主机进程状态信息读取失败
	BeaterProcStdConnDetectError = 1403 // 标准化模式主机套接字信息读取失败
	BeaterProcNetConnDetectError = 1404 // Netlink模式主机套接字信息读取失败

	BeatPingDNSResolveOuterError = 2101 // DNS解析失败
	BeatPingInvalidIPOuterError  = 2102 // IP 格式异常

	BeaterProcPIDFileNotFountOuterError = 2401 // PID文件不存在
	BeaterProcStateReadOuterError       = 2402 // 单个进程状态信息读取失败
	BeaterProcNotMatchedOuterError      = 2403 // 进程关键字未匹配到任何进程

	CodeScriptRunOuterError        = 2301 // 脚本运行报错
	CodeScriptPromFormatOuterError = 2302 // 脚本打印的 Prom 数据格式异常
	CodeScriptNoOutputError        = 2303 // 脚本没有输出内容
	CodeScriptTimeoutError         = 2304 // 脚本执行超时
)

var CodeNameMap = map[BeatErrorCode]string{
	BeatErrCodeOK:      "正常",
	BeatErrCodeUnknown: "未知错误",
	BeatErrCodeCancel:  "取消",
	BeatErrCodeTimeout: "超时",
	BeatErrInternalErr: "系统内部异常",

	BeatErrCodeConnError:           "连接失败",
	BeatErrCodeConnTimeoutError:    "连接超时",
	BeatErrCodeConnProxyError:      "连接代理失败",
	BeatErrCodeConnDNSResolveError: "连接DNS解析失败",
	BeatErrCodeDNSResolveError:     "DNS解析失败",
	BeatInvalidIPError:             "非法IP地址",

	BeatErrCodeRequestError:         "请求失败",
	BeatErrCodeRequestTimeoutError:  "请求超时",
	BeatErrCodeRequestDeadLineError: "超时设置错误",
	BeatErrCodeRequestInitError:     "请求初始化失败",

	BeatErrCodeResponseError:          "响应失败",
	BeatErrCodeResponseTimeoutError:   "响应超时",
	BeatErrCodeResponseMatchError:     "匹配失败",
	BeatErrCodeResponseCodeError:      "响应码不匹配",
	BeatErrCodeResponseTemporaryError: "临时响应失败",
	BeatErrCodeResponseNoRspError:     "服务无响应",
	BeatErrCodeResponseHandleError:    "响应处理失败",
	BeatErrCodeResponseConnRefused:    "链接拒绝",
	BeatErrCodeResponseReadError:      "响应读取失败",
	BeatErrCodeResponseEmptyError:     "响应头部为空",
	BeatErrCodeResponseHeaderError:    "响应头部不符合",
	BeatErrCodeResponseNotFindIpv4:    "未找到ipv4地址",
	BeatErrCodeResponseNotFindIpv6:    "未找到ipv6地址",
	BeatErrCodeResponseParseUrlErr:    "url解析错误",

	BeatErrScriptTsUnitConfigError: "脚本配置中的时间单位设置异常",

	BeaterProcSnapshotReadError:  "主机进程状态信息读取失败",
	BeaterProcStdConnDetectError: "标准化模式主机套接字信息读取失败",
	BeaterProcNetConnDetectError: "Netlink模式主机套接字信息读取失败",

	BeatPingDNSResolveOuterError: "DNS解析失败",
	BeatPingInvalidIPOuterError:  "IP 格式异常",

	BeaterProcPIDFileNotFountOuterError: "PID文件不存在",
	BeaterProcStateReadOuterError:       "单个进程状态信息读取失败",
	BeaterProcNotMatchedOuterError:      "进程关键字未匹配到任何进程",

	CodeScriptRunOuterError:        "脚本运行报错",
	CodeScriptPromFormatOuterError: "脚本打印的 Prom 数据格式异常",
	CodeScriptNoOutputError:        "脚本没有输出内容",
	CodeScriptTimeoutError:         "脚本执行超时",
}
