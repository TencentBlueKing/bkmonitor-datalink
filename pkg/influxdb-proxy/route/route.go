// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package route

import (
	"net/http"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/logging"
)

// RawQuery 透传查询
var RawQuery = func(flow uint64, request *http.Request, flowLog *logging.Entry) (*http.Response, error) {
	return routeManager.rawQueryExecution(flow, request, flowLog)
}

// Query :
var Query = func(queryParams *QueryParams, flowLog *logging.Entry) *ExecuteResult {
	executionFunc := routeManager.getQueryExecution(queryParams, flowLog)
	return executionFunc(queryParams, flowLog)
}

// Write :
var Write = func(writeParams *WriteParams, flowLog *logging.Entry) *ExecuteResult {
	executionFunc := routeManager.getWriteExecution(writeParams, flowLog)
	return executionFunc(writeParams, flowLog)
}

// CreateDB :
var CreateDB = func(createDBParams *CreateDBParams, flowLog *logging.Entry) *ExecuteResult {
	executionFunc := routeManager.getCreateDBExecution(createDBParams, flowLog)
	return executionFunc(createDBParams, flowLog)
}
