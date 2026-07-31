// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package decoder

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver/datadogrum/internal/model"
)

func TestReadBodyAtLimit(t *testing.T) {
	req := httptestRequest(bytes.NewBufferString("12345"), "")
	body, err := ReadBody(req, 5)
	assert.NoError(t, err)
	assert.Equal(t, "12345", body.String())
}

func TestReadBodyLimit(t *testing.T) {
	req := httptestRequest(bytes.NewBufferString("12345"), "")
	_, err := ReadBody(req, 4)
	assert.ErrorIs(t, err, ErrBodyTooLarge)
}

func TestReadBodyGzipLimit(t *testing.T) {
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	_, _ = zw.Write([]byte("12345"))
	assert.NoError(t, zw.Close())

	req := httptestRequest(bytes.NewReader(compressed.Bytes()), encodingGzip)
	_, err := ReadBody(req, 4)
	assert.ErrorIs(t, err, ErrBodyTooLarge)
}

func TestReadBodyGzipEncodingIsCaseAndWhitespaceInsensitive(t *testing.T) {
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	_, _ = zw.Write([]byte("12345"))
	assert.NoError(t, zw.Close())

	for _, encoding := range []string{"GZIP", " gzip ", " GzIp "} {
		req := httptestRequest(bytes.NewReader(compressed.Bytes()), encoding)
		body, err := ReadBody(req, 5)
		assert.NoError(t, err, encoding)
		assert.Equal(t, "12345", body.String(), encoding)
	}
}

func TestReadBodyInvalidLimit(t *testing.T) {
	req := httptestRequest(bytes.NewBufferString("1"), "")
	for _, limit := range []int64{0, -1} {
		_, err := ReadBody(req, limit)
		assert.ErrorIs(t, err, ErrInvalidLimit)
	}
}

func TestReadBodyNilBody(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, "/v1/rum", nil)
	_, err := ReadBody(req, 1024)
	assert.ErrorIs(t, err, model.ErrEmptyBody)
}

func httptestRequest(body io.Reader, encoding string) *http.Request {
	req, _ := http.NewRequest(http.MethodPost, "/v1/rum", body)
	if encoding != "" {
		req.Header.Set("Content-Encoding", encoding)
	}
	return req
}
