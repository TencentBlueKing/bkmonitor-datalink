// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package diffutil

import (
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
)

func NewSqlBody(tableName string, params map[string]any) SQLBody {
	return SQLBody{TableName: tableName, Params: params}
}

// SQLBody DB类body
type SQLBody struct {
	TableName string         `json:"table_name"`
	Params    map[string]any `json:"params"`
}

// String 转化为string
func (s SQLBody) String() string {
	jsonStr, _ := jsonx.MarshalString(s)
	return jsonStr
}

// NewStringBody new StringBody
func NewStringBody(body string) StringBody {
	return StringBody{Body: body}
}

// StringBody 字符串类型body
type StringBody struct {
	Body string
}

// String 转化为string
func (s StringBody) String() string {
	return s.Body
}

const (
	OperatorTypeDBCreate = "create"
	OperatorTypeDBUpdate = "update"
	OperatorTypeDBDelete = "delete"

	OperatorTypeAPIGet    = "get"
	OperatorTypeAPIPost   = "post"
	OperatorTypeAPIPut    = "put"
	OperatorTypeAPIDelete = "delete"
)

// LogBuilder 用于生成打点的日志
type LogBuilder struct {
	TaskName string       `json:"task_name"`
	Operator string       `json:"operator"`
	Body     fmt.Stringer `json:"body"`
	RespData string       `json:"respData"`
}

// String 转化为string
func (s LogBuilder) String() string {
	return fmt.Sprintf("task_name: %s, operator: %s, body: %s, resp_data: %s", s.TaskName, s.Operator, s.Body.String(), s.RespData)
}

type Stringer interface {
	String() string
}

// BuildLogStr 生成打点日志
func BuildLogStr(taskName, operator string, body Stringer, respData string) string {
	return LogBuilder{
		TaskName: taskName,
		Operator: operator,
		Body:     body,
		RespData: respData,
	}.String()
}
