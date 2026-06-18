// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package httpmiddleware

import (
	"net/http"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/throttle"
)

const retryAfterSeconds = "1" // 429 的 Retry-After，提示客户端 1s 后再来

func init() {
	Register("throttle", Throttle)
}

// Throttle 是 HTTP 限流中间件：按请求路径归类后交给全局单例裁决。
func Throttle(_ string) MiddlewareFunc { // 入参是 optmap 串，这里用不上，结构化配置走 receiver.throttle
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !throttle.Enabled() {
				next.ServeHTTP(w, r)
				return
			}

			// 未注册的端点不归类、不限流。
			recordType := throttle.ClassifyHTTP(r.URL.Path)
			if recordType == define.RecordUndefined {
				next.ServeHTTP(w, r)
				return
			}

			action := throttle.GlobalManager().Decide(recordType)
			throttle.IncRequest(define.RequestHttp, recordType, action)
			if action == throttle.ActionAdmit {
				next.ServeHTTP(w, r)
				return
			}

			w.Header().Set("Retry-After", retryAfterSeconds)
			http.Error(w, "collector overloaded", http.StatusTooManyRequests)
		})
	}
}
