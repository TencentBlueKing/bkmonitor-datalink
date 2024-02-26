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
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseNReadField(t *testing.T) {
	anyFieldName := "foo_field_name"
	fileName := "anything.pprof"
	fileContent := []byte("something here")

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	fw, err := writer.CreateFormFile(anyFieldName, fileName)
	fw.Write(fileContent)
	if err != nil {
		t.Fatal(err)
	}
	writer.Close()

	contentType := writer.FormDataContentType()
	boundary, err := ParseBoundary(contentType)
	if err != nil {
		t.Fatal(err)
	}

	form, err := multipart.NewReader(body, boundary).ReadForm(32 << 20)
	if err != nil {
		t.Fatal(err)
	}

	readContent, err := ReadField(form, anyFieldName)
	if err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, fileContent, readContent)
}

func TestExtraApplicationNameAndTags(t *testing.T) {
	t.Run("valid data", func(t *testing.T) {
		validUrl, err := http.NewRequest(http.MethodPost, "http://localhost/pyroscope/ingest?name=profiling-test%7Bcustom1%3D123456%2CserviceName%3Dmy-profiling-proj%7D&units=samples&aggregationType=sum&sampleRate=100&from=1708585375&until=1708585385&spyName=javaspy&format=jfr", &bytes.Buffer{})
		assert.NoError(t, err)
		appName, tags := getApplicationNameAndTags(validUrl)
		assert.Equal(t, appName, "profiling-test")
		assert.Equal(t, tags, map[string]string{"custom1": "123456", "serviceName": "my-profiling-proj"})
	})

	t.Run("empty tags", func(t *testing.T) {
		validUrl, err := http.NewRequest(http.MethodPost, "http://localhost/pyroscope/ingest?name=profiling-test%7B%7D&units=samples&aggregationType=sum&sampleRate=100&from=1708585375&until=1708585385&spyName=javaspy&format=jfr", &bytes.Buffer{})
		assert.NoError(t, err)
		appName, tags := getApplicationNameAndTags(validUrl)
		assert.Equal(t, appName, "profiling-test")
		assert.Empty(t, tags)
	})

	t.Run("empty tags + empty app", func(t *testing.T) {
		validUrl, err := http.NewRequest(http.MethodPost, "http://localhost/pyroscope/ingest?units=samples&aggregationType=sum&sampleRate=100&from=1708585375&until=1708585385&spyName=javaspy&format=jfr", &bytes.Buffer{})
		assert.NoError(t, err)
		appName, tags := getApplicationNameAndTags(validUrl)
		assert.Empty(t, appName)
		assert.Empty(t, tags)
	})

	// test4: invalid tags
	t.Run("invalid tags", func(t *testing.T) {
		validUrl, err := http.NewRequest(http.MethodPost, "http://localhost/pyroscope/ingest?name=profiling-test%7B%7D%7D%7D&units=samples&aggregationType=sum&sampleRate=100&from=1708585375&until=1708585385&spyName=javaspy&format=jfr", &bytes.Buffer{})
		assert.NoError(t, err)
		appName, tags := getApplicationNameAndTags(validUrl)
		assert.Equal(t, appName, "profiling-test")
		assert.Empty(t, tags)
	})
}
