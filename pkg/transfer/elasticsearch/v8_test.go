// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build elasticsearch_v8
// +build elasticsearch_v8

package elasticsearch

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/elastic/go-elasticsearch/v8/esapi"
	"github.com/stretchr/testify/require"
)

type captureTransport struct {
	body string
}

func (t *captureTransport) Perform(request *http.Request) (*http.Response, error) {
	data, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}
	t.body = string(data)
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(`{"errors":false,"items":[]}`)),
	}, nil
}

func TestESv8WriterRegistrationAndBulkMetadata(t *testing.T) {
	_, ok := writers["v8"]
	require.True(t, ok)

	transport := &captureTransport{}
	writer := &ESv8Writer{
		ESWriter: NewESWriter(transport),
		request:  esapi.BulkRequest{},
	}
	record := NewRecord(map[string]interface{}{"type": "business-type", "message": "test"})
	record.SetID("deterministic-id")
	record.SetType("legacy-mapping-type")

	response, err := writer.Write(context.Background(), "logs-index", Records{record})
	require.NoError(t, err)
	require.NotNil(t, response)
	require.NoError(t, response.Body.Close())
	require.Contains(t, transport.body, `"_id":"deterministic-id"`)
	require.NotContains(t, transport.body, `"_type"`)
	require.Contains(t, transport.body, `"type":"business-type"`)
	require.Equal(t, "legacy-mapping-type", record.Meta["_type"])
}
