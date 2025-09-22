// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package errno

// 错误定义结构 - 直接定义每个错误的所有信息
type ErrorDefinition struct {
	Code     string // 错误代码，如 "QP001"
	Message  string // 错误消息，如 "SQL语法错误"
	Category string // 错误分类，如 "查询解析"
	Severity string // 严重程度，如 "error"
}

// 统一的错误定义映射表 - 每个错误一个独立定义，实现一对一映射
var errorDefinitions = map[string]ErrorDefinition{
	// 查询解析类错误 (Query Parse - QP)
	"ErrQueryParseInvalidSQL":       {"QP001", "SQL语法错误", "查询解析", "error"},
	"ErrQueryParseInvalidPromQL":    {"QP002", "PromQL语法错误", "查询解析", "error"},
	"ErrQueryParseInvalidField":     {"QP003", "字段名称无效", "查询解析", "error"},
	"ErrQueryParseInvalidCondition": {"QP004", "查询条件格式错误", "查询解析", "error"},

	// 存储连接类错误 (Storage Connection - SC)
	"ErrStorageConnFailed": {"SC001", "存储连接失败", "存储连接", "error"},

	// 数据处理类错误 (Data Processing - DP)
	"ErrDataProcessFailed":     {"DP001", "数据处理失败", "数据处理", "error"},
	"ErrDataFormatInvalid":     {"DP002", "数据格式错误", "数据处理", "error"},
	"ErrDataDeserializeFailed": {"DP003", "数据反序列化失败", "数据处理", "error"},

	// 配置管理类错误 (Configuration - CF)
	"ErrConfigReloadFailed": {"CF001", "配置重载失败", "配置管理", "error"},

	// 业务逻辑类错误 (Business Logic - BL)
	"ErrBusinessParamInvalid":   {"BL001", "业务参数无效", "业务逻辑", "error"},
	"ErrBusinessLogicError":     {"BL002", "业务逻辑错误", "业务逻辑", "error"},
	"ErrBusinessQueryExecution": {"BL003", "业务查询执行失败", "业务逻辑", "error"},

	// 警告类错误 (Warning - WN)
	"ErrWarningConfigDegraded":  {"WN001", "配置降级处理", "警告", "warning"},
	"ErrWarningDataIncomplete":  {"WN002", "数据不完整", "警告", "warning"},
	"ErrWarningServiceDegraded": {"WN003", "服务降级", "警告", "warning"},
}

func newError(name string) *ErrCode {
	def, exists := errorDefinitions[name]
	if !exists {
		return NewErrCode("UNKNOWN", "未知错误", "未知", "error")
	}

	return NewErrCode(def.Code, def.Message, def.Category, def.Severity)
}

func ErrQueryParseInvalidSQL() *ErrCode       { return newError("ErrQueryParseInvalidSQL") }
func ErrQueryParseInvalidPromQL() *ErrCode    { return newError("ErrQueryParseInvalidPromQL") }
func ErrQueryParseInvalidField() *ErrCode     { return newError("ErrQueryParseInvalidField") }
func ErrQueryParseInvalidCondition() *ErrCode { return newError("ErrQueryParseInvalidCondition") }
func ErrStorageConnFailed() *ErrCode          { return newError("ErrStorageConnFailed") }
func ErrDataProcessFailed() *ErrCode          { return newError("ErrDataProcessFailed") }
func ErrDataFormatInvalid() *ErrCode          { return newError("ErrDataFormatInvalid") }
func ErrDataDeserializeFailed() *ErrCode      { return newError("ErrDataDeserializeFailed") }
func ErrConfigReloadFailed() *ErrCode         { return newError("ErrConfigReloadFailed") }
func ErrBusinessParamInvalid() *ErrCode       { return newError("ErrBusinessParamInvalid") }
func ErrBusinessLogicError() *ErrCode         { return newError("ErrBusinessLogicError") }
func ErrBusinessQueryExecution() *ErrCode     { return newError("ErrBusinessQueryExecution") }
func ErrWarningConfigDegraded() *ErrCode      { return newError("ErrWarningConfigDegraded") }
func ErrWarningDataIncomplete() *ErrCode      { return newError("ErrWarningDataIncomplete") }
func ErrWarningServiceDegraded() *ErrCode     { return newError("ErrWarningServiceDegraded") }
