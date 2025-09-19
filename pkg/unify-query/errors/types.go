// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package errors

const (
	// 查询解析类错误 (QP - Query Parse)
	ErrQueryParseInvalidSQL    = "SQL语法错误"      // QP001
	ErrQueryParseUnsupported   = "不支持的查询语法" // QP002
	ErrQueryParseTimeout       = "查询解析超时"     // QP003
	ErrQueryParseFieldMissing  = "缺少必要字段"     // QP004
	ErrQueryParseResultInvalid = "查询结果解析失败" // QP005

	// 存储连接类错误 (SC - Storage Connection)
	ErrStorageConnFailed        = "存储连接失败" // SC001
	ErrStorageConnTimeout       = "存储连接超时" // SC002
	ErrStorageConnPoolExhausted = "连接池耗尽"   // SC003
	ErrStorageConnConfig        = "存储配置错误" // SC004

	// 权限认证类错误 (AU - Authentication)
	ErrAuthTokenInvalid      = "Token无效"   // AU001
	ErrAuthTokenExpired      = "Token已过期" // AU002
	ErrAuthPermissionDenied  = "权限不足"    // AU003
	ErrAuthSpaceUnauthorized = "空间未授权"  // AU004

	// 性能问题类错误 (PF - Performance)
	ErrPerformanceSlowQuery  = "慢查询检测"   // PF001
	ErrPerformanceMemoryHigh = "内存使用过高" // PF002
	ErrPerformanceTimeout    = "查询执行超时" // PF003

	// 服务配置类错误 (CF - Config)
	ErrConfigConsulFailed     = "Consul配置加载失败" // CF001
	ErrConfigReloadFailed     = "配置重载失败"       // CF002
	ErrConfigValidationFailed = "配置验证失败"       // CF003
	ErrConfigServiceRestart   = "服务重启配置"       // CF004
	ErrConfigServiceReady     = "服务启动就绪"       // CF005

	// 数据处理类错误 (DP - Data Process)
	ErrDataProcessTypeMismatch  = "数据类型不匹配"   // DP001
	ErrDataProcessFormatInvalid = "数据格式无效"     // DP002
	ErrDataProcessEmpty         = "数据为空"         // DP003
	ErrDataProcessSerialize     = "数据序列化失败"   // DP004
	ErrDataProcessDeserialize   = "数据反序列化失败" // DP005
	ErrDataProcessLoad          = "数据加载失败"     // DP006
	ErrDataProcessMockEmpty     = "Mock数据为空"     // DP007
	ErrDataProcessFailed        = "数据处理失败"     // DP008

	// 网络请求类错误 (NW - Network)
	ErrNetworkConnFailed       = "网络连接失败" // NW001
	ErrNetworkTimeout          = "网络请求超时" // NW002
	ErrNetworkDNSResolveFailed = "DNS解析失败"  // NW003

	// 业务逻辑类错误 (BZ - Business)
	ErrBusinessParamInvalid    = "参数无效"     // BZ001
	ErrBusinessDataNotFound    = "数据未找到"   // BZ002
	ErrBusinessOperationFailed = "操作执行失败" // BZ003
	ErrBusinessQueryExecution  = "查询执行失败" // BZ004
	ErrBusinessRequestProcess  = "请求处理失败" // BZ005
	ErrBusinessTestFailure     = "测试执行失败" // BZ006
	ErrBusinessCMDBOperation   = "CMDB操作失败" // BZ007

	// 警告类型 (WN - Warning)
	ErrWarningConfigDegraded   = "配置降级处理" // WN001
	ErrWarningDataIncomplete   = "数据不完整"   // WN002
	ErrWarningServiceDegraded  = "服务降级"     // WN003
	ErrWarningPerformanceSlow  = "性能警告"     // WN004
)

var ErrorCodeMap = map[string]string{
	ErrQueryParseInvalidSQL:    "QP001",
	ErrQueryParseUnsupported:   "QP002",
	ErrQueryParseTimeout:       "QP003",
	ErrQueryParseFieldMissing:  "QP004",
	ErrQueryParseResultInvalid: "QP005",

	ErrStorageConnFailed:        "SC001",
	ErrStorageConnTimeout:       "SC002",
	ErrStorageConnPoolExhausted: "SC003",
	ErrStorageConnConfig:        "SC004",

	ErrAuthTokenInvalid:      "AU001",
	ErrAuthTokenExpired:      "AU002",
	ErrAuthPermissionDenied:  "AU003",
	ErrAuthSpaceUnauthorized: "AU004",

	ErrPerformanceSlowQuery:  "PF001",
	ErrPerformanceMemoryHigh: "PF002",
	ErrPerformanceTimeout:    "PF003",

	ErrConfigConsulFailed:     "CF001",
	ErrConfigReloadFailed:     "CF002",
	ErrConfigValidationFailed: "CF003",
	ErrConfigServiceRestart:   "CF004",
	ErrConfigServiceReady:     "CF005",

	ErrDataProcessTypeMismatch:  "DP001",
	ErrDataProcessFormatInvalid: "DP002",
	ErrDataProcessEmpty:         "DP003",
	ErrDataProcessSerialize:     "DP004",
	ErrDataProcessDeserialize:   "DP005",
	ErrDataProcessLoad:          "DP006",
	ErrDataProcessMockEmpty:     "DP007",
	ErrDataProcessFailed:        "DP008",

	ErrNetworkConnFailed:       "NW001",
	ErrNetworkTimeout:          "NW002",
	ErrNetworkDNSResolveFailed: "NW003",

	ErrBusinessParamInvalid:    "BZ001",
	ErrBusinessDataNotFound:    "BZ002",
	ErrBusinessOperationFailed: "BZ003",
	ErrBusinessQueryExecution:  "BZ004",
	ErrBusinessRequestProcess:  "BZ005",
	ErrBusinessTestFailure:     "BZ006",
	ErrBusinessCMDBOperation:   "BZ007",

	ErrWarningConfigDegraded:   "WN001",
	ErrWarningDataIncomplete:   "WN002",
	ErrWarningServiceDegraded:  "WN003",
	ErrWarningPerformanceSlow:  "WN004",
}

func GetErrorCode(errType string) string {
	if code, exists := ErrorCodeMap[errType]; exists {
		return code
	}
	return "UNKNOWN"
}
