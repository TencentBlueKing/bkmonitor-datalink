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

// GetErrorCodeByError 通过错误信息获取错误代码
func GetErrorCodeByError(err error) int {
	var val int
	var ok bool
	if val, ok = ErrCodeMap[err]; !ok {
		return ErrCodeMap[ErrNotConfigured]
	}
	return val
}

var ErrCodeMap = map[error]int{
	ErrNotConfigured:   100,
	ErrTaskNotFound:    103,
	ErrType:            106,
	ErrNoChildPath:     111,
	ErrGetChildTasks:   112,
	ErrUnpackCfg:       113,
	ErrCleanGlobalFail: 114,
	ErrGetTaskFailed:   121,
	ErrNoName:          122,
	ErrNoVersion:       123,
	ErrTypeConvert:     131,
	ErrWrongTargetType: 138,
}

var (
	ErrNotConfigured   = errors.New("get not configured error")
	ErrTaskNotFound    = errors.New("task not found")
	ErrType            = errors.New("type error")
	ErrTypeConvert     = errors.New("get error when try to convert type")
	ErrWrongTargetType = errors.New("wrong target type") // 目标类型不是ip也不是domain
	ErrNoChildPath     = errors.New("bkmonitorbeat.include not configured")
	ErrGetChildTasks   = errors.New("get child tasks error")
	ErrUnpackCfg       = errors.New("unpack cfg error")
	ErrCleanGlobalFail = errors.New("clean global config failed")
	ErrGetTaskFailed   = errors.New("get task item by filename failed")
	ErrNoName          = errors.New("task name not found")
	ErrNoVersion       = errors.New("task version not found")
	ErrorNoTask        = errors.New("no task found")
	ErrNoScriptOutput  = errors.New("script no output")
)
