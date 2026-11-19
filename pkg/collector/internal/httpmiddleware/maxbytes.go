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
	"io"
	"net/http"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/optmap"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	defaultMaxRequestBytes = 1024 * 1024 * 200 // 200MB
	optMaxRequestBytes     = "maxRequestBytes"
)

func init() {
	Register("maxbytes", MaxBytes)
}

func MaxBytes(opt string) MiddlewareFunc {
	om := optmap.New(opt)
	n := om.GetIntDefault(optMaxRequestBytes, defaultMaxRequestBytes)
	logger.Infof("maxbytes middleware opts: %s(%d)", optMaxRequestBytes, n)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ContentLength > int64(n) {
				logger.Errorf("request entity too large, limit is %d, but got %d", n, r.ContentLength)
				define.IncReceiverRejected(define.RequestHttp, define.RejectTooLarge)
				w.WriteHeader(http.StatusRequestEntityTooLarge)
				return
			}
			// A chunked body has no length to check up front. Its bytes are
			// counted as the handler reads them and a body past the limit is
			// counted once, but it is still read in full: refusing it here
			// would make the handler fail halfway through the body.
			if r.ContentLength < 0 && r.Body != nil && r.Body != http.NoBody {
				r.Body = &chunkedBody{ReadCloser: r.Body, limit: int64(n)}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// chunkedBody passes a chunked request body through unchanged and counts a
// chunked_over_limit rejection the first time the bytes read exceed limit.
type chunkedBody struct {
	io.ReadCloser
	limit int64
	read  int64
	over  bool
}

func (b *chunkedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.read += int64(n)
	if !b.over && b.read > b.limit {
		b.over = true
		define.IncReceiverRejected(define.RequestHttp, define.RejectChunkedOverLimit)
	}
	return n, err
}
