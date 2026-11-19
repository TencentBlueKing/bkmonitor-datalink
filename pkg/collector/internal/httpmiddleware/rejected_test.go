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
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/metrictest"
)

func httpRejected(t *testing.T, reason string) float64 {
	t.Helper()
	v, _ := metrictest.Value(t, "bk_collector_receiver_rejected_total", map[string]string{"protocol": "http", "reason": reason})
	return v
}

// readAll is a handler that reads the body four bytes at a time, as a
// decoder reading a stream does, and keeps what it read.
type readAll struct {
	got []byte
	te  []string
}

func (h *readAll) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.te = r.TransferEncoding
	buf := make([]byte, 4)
	for {
		n, err := r.Body.Read(buf)
		h.got = append(h.got, buf[:n]...)
		if err != nil {
			break
		}
	}
	w.WriteHeader(http.StatusOK)
}

// A body whose length is over the limit is answered 413 without reaching
// the handler and counted once as too_large; one at the limit goes through.
func TestMaxBytesCountsABodyOverTheLimit(t *testing.T) {
	h := &readAll{}
	srv := Get("maxbytes;maxRequestBytes=16")(h)

	before := httpRejected(t, "too_large")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(strings.Repeat("x", 17))))
	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	assert.Empty(t, h.got)
	assert.Equal(t, before+1, httpRejected(t, "too_large"))

	w = httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(strings.Repeat("y", 16))))
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, strings.Repeat("y", 16), string(h.got))
	assert.Equal(t, before+1, httpRejected(t, "too_large"))
}

// unsized hides the length of what it reads, so the client sends it chunked.
type unsized struct{ io.Reader }

// A chunked body has no length the limit can be checked against before it
// is read. One that reads past the limit is counted once as
// chunked_over_limit, however many reads it takes, and still reaches the
// handler whole; one that stays within the limit is not counted.
func TestMaxBytesCountsAChunkedBodyPastTheLimitAndPassesItOn(t *testing.T) {
	h := &readAll{}
	ts := httptest.NewServer(Get("maxbytes;maxRequestBytes=16")(h))
	defer ts.Close()

	post := func(body string) int {
		req, err := http.NewRequest(http.MethodPost, ts.URL, unsized{strings.NewReader(body)})
		require.NoError(t, err)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		resp.Body.Close()
		require.Equal(t, []string{"chunked"}, h.te)
		return resp.StatusCode
	}

	before := httpRejected(t, "chunked_over_limit")
	tooLarge := httpRejected(t, "too_large")
	body := strings.Repeat("z", 40)
	assert.Equal(t, http.StatusOK, post(body))
	assert.Equal(t, body, string(h.got))
	assert.Equal(t, before+1, httpRejected(t, "chunked_over_limit"))
	assert.Equal(t, tooLarge, httpRejected(t, "too_large"))

	h.got = nil
	assert.Equal(t, http.StatusOK, post(strings.Repeat("w", 16)))
	assert.Equal(t, strings.Repeat("w", 16), string(h.got))
	assert.Equal(t, before+1, httpRejected(t, "chunked_over_limit"))
}

// A body marked gzip or zlib that does not decompress is answered 400 and
// counted as decompress; a body that does decompress is not counted.
func TestContentDecompressorCountsABodyThatDoesNotDecompress(t *testing.T) {
	h := &readAll{}
	srv := Get("content_decompressor")(h)

	send := func(encoding string, body []byte) int {
		req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
		req.Header.Set("Content-Encoding", encoding)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		return w.Code
	}

	before := httpRejected(t, "decompress")
	assert.Equal(t, http.StatusBadRequest, send("gzip", []byte("not gzip at all")))
	assert.Equal(t, http.StatusBadRequest, send("deflate", []byte("not zlib either")))
	assert.Empty(t, h.got)
	assert.Equal(t, before+2, httpRejected(t, "decompress"))

	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, err := zw.Write([]byte("compressed spans"))
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	assert.Equal(t, http.StatusOK, send("gzip", buf.Bytes()))
	assert.Equal(t, "compressed spans", string(h.got))
	assert.Equal(t, before+2, httpRejected(t, "decompress"))
}
