// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package jfr

import (
	"bytes"
	"io"
	"os"

	"github.com/klauspost/pgzip"
	"github.com/pkg/errors"
)

// Decompress 如果 body 为压缩包格式，则进行解压缩并读取
func Decompress(bs []byte) ([]byte, error) {
	if len(bs) >= 2 && bs[0] == 0x1f && bs[1] == 0x8b {
		gzipReader, err := pgzip.NewReader(bytes.NewReader(bs))
		if err != nil {
			return nil, errors.Wrap(err, "failed to read gzip header")
		}
		defer gzipReader.Close()

		buf := bytes.NewBuffer(nil)
		if _, err = buf.ReadFrom(gzipReader); err != nil {
			return nil, errors.Wrap(err, "failed to decompress")
		}
		return buf.Bytes(), nil
	}
	return bs, nil
}

// ReadGzipFile 读取压缩包，返回文件内容
func ReadGzipFile(f string) ([]byte, error) {
	fd, err := os.Open(f)
	if err != nil {
		return nil, err
	}
	defer fd.Close()
	g, err := pgzip.NewReader(fd)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(g)
}
