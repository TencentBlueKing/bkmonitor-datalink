// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package common

import (
	"math/rand"
	"net/http"
	"strconv"
	"time"
)

var moduleName = "flow"

func init() {
	rand.Seed(time.Now().Unix())
}

// GetFlow 获取flow id,这个id用于追踪整个执行流程的执行信息,先尝试从header中拿，拿不到就自己初始化一个
func GetFlow(request *http.Request) uint64 {
	var flowID uint64
	var err error
	// 检查一下request的header，如果有traceid就直接用,没有就rand.Uint64
	if request != nil {
		xTraceID := request.Header.Get("X-Trace-ID")
		if xTraceID != "" {
			flowID, err = strconv.ParseUint(xTraceID, 0, 64)
			if err != nil {
				flowID = rand.Uint64()
				return flowID
			}
			return flowID
		}
	}
	flowID = rand.Uint64()
	// 将flowid赋值到头部，提供给其他组件使用
	request.Header.Set("X-Trace-ID", strconv.FormatUint(flowID, 10))
	return flowID
}
