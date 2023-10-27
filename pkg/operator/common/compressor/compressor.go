// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package compressor

import (
	"bytes"
	"compress/gzip"
	"io"
)

type Compressor interface {
	Compress(b []byte) ([]byte, error)
	Uncompress(b []byte) ([]byte, error)
}

func Compress(b []byte) ([]byte, error) {
	return defaultCompressor.Compress(b)
}

func Uncompress(b []byte) ([]byte, error) {
	return defaultCompressor.Uncompress(b)
}

var defaultCompressor = gzipCompressor{}

type gzipCompressor struct{}

func (gzipCompressor) Compress(b []byte) ([]byte, error) {
	buf := &bytes.Buffer{}
	w := gzip.NewWriter(buf)
	if _, err := w.Write(b); err != nil {
		w.Close()
		return nil, err
	}
	w.Close()
	return buf.Bytes(), nil
}

func (gzipCompressor) Uncompress(conf []byte) ([]byte, error) {
	reader := bytes.NewReader(conf)
	r, err := gzip.NewReader(reader)
	if err != nil {
		return nil, err
	}
	buf := &bytes.Buffer{}
	if _, err := io.Copy(buf, r); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
