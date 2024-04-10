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
	"compress/gzip"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDecompress(t *testing.T) {
	t.Run("empty input", func(t *testing.T) {
		var input []byte
		output, err := Decompress(input)
		assert.NoError(t, err)
		assert.Equal(t, output, input)
	})

	t.Run("non-gzip input", func(t *testing.T) {
		input := []byte{0x00, 0x01, 0x02, 0x03}
		output, err := Decompress(input)
		assert.NoError(t, err)
		assert.Equal(t, output, input)
	})

	t.Run("invalid data", func(t *testing.T) {
		input := []byte{0x1f, 0x8b, 0x00}
		_, err := Decompress(input)
		assert.Error(t, err)
	})

	t.Run("valid header + invalid data", func(t *testing.T) {
		input := []byte{0x1f, 0x8b, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xff, 0x00}
		_, err := Decompress(input)
		assert.Error(t, err)
	})

	t.Run("valid input", func(t *testing.T) {
		originalData := []byte("Hello, world!")
		var gzipBuffer bytes.Buffer
		gzipWriter := gzip.NewWriter(&gzipBuffer)
		_, err := gzipWriter.Write(originalData)
		assert.NoError(t, err)
		if err := gzipWriter.Close(); err != nil {
			t.Fatalf("Failed to close gzip writer: %v", err)
		}
		gzipData := gzipBuffer.Bytes()

		output, err := Decompress(gzipData)
		assert.NoError(t, err)
		assert.Equal(t, output, originalData)
	})
}

func TestReadGzipFile(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		f, err := os.CreateTemp("", "gzip_test1")
		assert.NoError(t, err)
		defer os.Remove(f.Name())

		gw := gzip.NewWriter(f)
		_, err = gw.Write([]byte("test data"))
		assert.NoError(t, err)
		assert.NoError(t, gw.Close())
		assert.NoError(t, f.Close())

		data, err := ReadGzipFile(f.Name())
		assert.NoError(t, err)
		assert.Equal(t, []byte("test data"), data)
	})

	t.Run("Failed", func(t *testing.T) {
		data, err := ReadGzipFile("/tmp/no_exist.file")
		assert.Error(t, err)
		assert.Nil(t, data)
	})
}
