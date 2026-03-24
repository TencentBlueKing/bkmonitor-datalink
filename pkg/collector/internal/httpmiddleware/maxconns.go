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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/optmap"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/semaphore"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	defaultMaxConnectionsRatio = 256
	optMaxConnectionsRatio     = "maxConnectionsRatio"
)

func init() {
	Register("maxconns", MaxConns)
}

func MaxConns(opt string) MiddlewareFunc {
	om := optmap.New(opt)
	n := om.GetIntDefault(optMaxConnectionsRatio, defaultMaxConnectionsRatio)
	logger.Infof("maxconns middleware opts: %s(%d)", optMaxConnectionsRatio, n)

	return func(next http.Handler) http.Handler {
		sem := semaphore.New(define.RequestHttp.S(), define.CoreNum()*n)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got := sem.AcquireWithTimeout(define.AcquireTimeout)
			if !got {
				logger.Warnf("%s: failed to get semaphore, ip=%v", sem, r.RemoteAddr)
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			defer sem.Release()

			logger.Debugf("maxconns semaphore count: %d", sem.Count())
			next.ServeHTTP(w, r)
		})
	}
}
