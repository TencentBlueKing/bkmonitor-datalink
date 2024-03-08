// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pyroscope

import (
	"bytes"
	"mime/multipart"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestReadField(t *testing.T) {
	anyFieldName := "foo_field_name"
	fileName := "anything.pprof"
	fileContent := []byte("something here")

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	fw, err := writer.CreateFormFile(anyFieldName, fileName)
	assert.NoError(t, err)

	_, err = fw.Write(fileContent)
	assert.NoError(t, err)
	assert.NoError(t, writer.Close())

	contentType := writer.FormDataContentType()
	boundary, err := ParseBoundary(contentType)
	assert.NoError(t, err)

	form, err := multipart.NewReader(body, boundary).ReadForm(32 << 20)
	assert.NoError(t, err)

	readContent, err := ReadField(form, anyFieldName)
	assert.NoError(t, err)
	assert.Equal(t, fileContent, readContent)
}

func TestParseBoundary(t *testing.T) {
	t.Run("Invalid", func(t *testing.T) {
		boundary, err := ParseBoundary("")
		assert.Error(t, err)
		assert.Empty(t, boundary)
	})
}
