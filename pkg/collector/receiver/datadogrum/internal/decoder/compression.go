// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package decoder

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver/datadogrum/internal/model"
)

const encodingGzip = "gzip"

// ReadBody reads the request body, transparently decompressing gzip.
// maxBytes applies to the decompressed body.
func ReadBody(req *http.Request, maxBytes int64) (*bytes.Buffer, error) {
	if maxBytes <= 0 {
		return nil, ErrInvalidLimit
	}
	if req.Body == nil {
		return nil, model.ErrEmptyBody
	}

	var bodyReader io.Reader = req.Body
	if strings.EqualFold(strings.TrimSpace(req.Header.Get("Content-Encoding")), encodingGzip) {
		gzipReader, err := gzip.NewReader(req.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to create gzip reader: %w", err)
		}
		defer gzipReader.Close()
		bodyReader = gzipReader
	}

	bodyBuffer := &bytes.Buffer{}
	// Read one extra byte so we can detect whether the body exceeds maxBytes.
	copyLimit := maxBytes + 1
	if copyLimit <= maxBytes {
		copyLimit = maxBytes
	}
	if _, err := io.Copy(bodyBuffer, io.LimitReader(bodyReader, copyLimit)); err != nil {
		return nil, err
	}
	if bodyBuffer.Len() == 0 {
		return nil, model.ErrEmptyBody
	}
	if int64(bodyBuffer.Len()) > maxBytes {
		return nil, fmt.Errorf("%w: exceeds %d bytes", ErrBodyTooLarge, maxBytes)
	}
	return bodyBuffer, nil
}
