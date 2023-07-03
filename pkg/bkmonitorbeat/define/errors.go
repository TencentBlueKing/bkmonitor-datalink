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
	"errors"
)

// BeatErrorCode : beat task error code
type BeatErrorCode int

// BeatErrorCodes
const (
	BeatErrCodeOK      = 0 // 成功
	BeatErrCodeUnknown = 1 // 未知
	BeatErrCodeCancel  = 2 // 取消
	BeatErrCodeTimeout = 3 // 超时

	BeatErrCodeConnError           = 1000 // 连接失败
	BeatErrCodeConnTimeoutError    = 1001 // 连接超时
	BeatErrCodeConnProxyError      = 1002 // 连接代理失败
	BeatErrCodeConnDNSResolveError = 1003 // 连接DNS解析失败
	BeatErrCodeDNSResolveError     = 1004 // DNS解析失败

	BeatErrCodeRequestError         = 2000 // 请求失败
	BeatErrCodeRequestTimeoutError  = 2001 // 请求超时
	BeatErrCodeRequestDeadLineError = 2002 // 超时设置错误
	BeatErrCodeRequestInitError     = 2003 // 请求初始化失败

	BeatErrCodeResponseError          = 3000 // 响应失败
	BeatErrCodeResponseTimeoutError   = 3001 // 响应超时
	BeatErrCodeResponseMatchError     = 3002 // 匹配失败
	BeatErrCodeResponseCodeError      = 3003 // 响应码不匹配
	BeatErrCodeResponseTemporaryError = 3004 // 临时响应失败
	BeatErrCodeResponseNoRspError     = 3005 // 服务无响应
	BeatErrCodeResponseHandleError    = 3006 // 响应处理失败
	BeatErrCodeResponseConnRefused    = 3007 // 链接拒绝
	BeatErrCodeResponseReadError      = 3008 // 响应读取失败
	BeatErrCodeResponseEmptyError     = 3009 // 响应头部为空
	BeatErrCodeResponseHeaderError    = 3010 // 响应头部不符合
	BeatErrCodeResponseNotFindIpv4    = 3011 // 未找到ipv4地址
	BeatErrCodeResponseNotFindIpv6    = 3012 // 未找到ipv6地址
	BeatErrCodeResponseParseUrlErr    = 3013 // url解析错误

	BeatErrScriptRunError          = 4001 // 脚本运行报错
	BeatErrScriptParamError        = 4002 // 脚本命令不合法
	BeatErrScriptFormatOutputError = 4003 //解析脚本标准输出报错
)

// GetErrorCodeByError 通过错误信息获取错误代码
func GetErrorCodeByError(err error) int {
	var val int
	var ok bool
	if val, ok = ErrCodeMap[err]; !ok {
		return ErrCodeMap[ErrNotConfigured]
	}
	return val

}

// ErrCodeMap 建立error与errorcode之间的关系
var ErrCodeMap = map[error]int{
	ErrNotConfigured:   100,
	ErrNotImplemented:  101,
	ErrConfigureNotSet: 102,
	ErrTaskNoutFound:   103,
	ErrKeyNoutFound:    104,
	ErrDataIDNotSet:    105,
	ErrType:            106,
	ErrValue:           107,
	ErrKey:             108,

	//Global errors
	ErrNoChildPath:     111,
	ErrGetChildTasks:   112,
	ErrUnpackCfgError:  113,
	ErrCleanGlobalFail: 114,

	// Child errors
	ErrGetTaskFailed: 121,
	ErrNoName:        122,
	ErrNoVersion:     123,
	ErrTaskRepeat:    124,

	//SimpleTask errors
	ErrTypeConvertError: 131,

	//Corefile Errors
	ErrNoEventID: 134,

	//Custom errors
	ErrNoPort: 135,
	ErrNoPath: 136,

	// Ping errors
	ErrNoTarget:        137,
	ErrWrongTargetType: 138,
}

// Errors
var (
	ErrNotConfigured   = errors.New("get not configured error")
	ErrNotImplemented  = errors.New("not implemented error")
	ErrConfigureNotSet = errors.New("configure not set")
	ErrTaskNoutFound   = errors.New("task not found")
	ErrKeyNoutFound    = errors.New("key not found")
	ErrDataIDNotSet    = errors.New("data not set")
	ErrType            = errors.New("type error")
	ErrValue           = errors.New("value error")
	ErrKey             = errors.New("key error")
)

// SimpleTask errors
var (
	// SimpleTask errors
	ErrTypeConvertError = errors.New("get error when try to convert type")

	// Corefile errors
	ErrNoEventID = errors.New("missing eventid")

	// custom
	ErrNoPort = errors.New("missing listen port")
	ErrNoPath = errors.New("missing listen path")

	// Ping Errors
	ErrNoTarget        = errors.New("no targets found")  //目标列表为空
	ErrWrongTargetType = errors.New("wrong target type") //目标类型不是ip也不是domain

	//GlobalConfig errors
	ErrNoChildPath     = errors.New("bkmonitorbeat.include not configured")
	ErrGetChildTasks   = errors.New("get child tasks error")
	ErrUnpackCfgError  = errors.New("Unpack cfg error")
	ErrCleanGlobalFail = errors.New("clean global config failed")

	// ChildConfig errors
	ErrGetTaskFailed = errors.New("get task item by filename failed")
	ErrNoName        = errors.New("task name not found")
	ErrNoVersion     = errors.New("task version not found")
	ErrTaskRepeat    = errors.New("task repeat")

	// metricbeat errors
	ErrorNoTask = errors.New("no task found")
)
