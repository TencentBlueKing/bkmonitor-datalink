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

// RawQueryExecution 透传请求接口
type RawQueryExecution func(request *http.Request, flowLog *logging.Entry) (*http.Response, error)

// QueryExecution 查询执行接口
type QueryExecution func(params *QueryParams, flowLog *logging.Entry) *ExecuteResult

// WriteExecution 写入执行接口
type WriteExecution func(params *WriteParams, flowLog *logging.Entry) *ExecuteResult

// CreateDBExecution 建库执行接口
type CreateDBExecution func(params *CreateDBParams, flowLog *logging.Entry) *ExecuteResult
