// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build elasticsearch_v8

package elasticsearch

import (
	"context"

	"github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/esapi"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/bufferpool"
)

type ESv8Writer struct {
	*ESWriter
	request esapi.BulkRequest
}

func NewESv8Writer(config map[string]interface{}) (BulkWriter, error) {
	var request esapi.BulkRequest
	if err := ApplyFields(&request, config); err != nil {
		return nil, err
	}
	var clientConfig elasticsearch.Config
	if err := ApplyFields(&clientConfig, config); err != nil {
		return nil, err
	}
	// 由 BulkHandler 按文档结果重试，避免客户端再次发送已经成功的条目。
	clientConfig.DisableRetry = true
	client, err := elasticsearch.NewClient(clientConfig)
	if err != nil {
		return nil, err
	}
	return &ESv8Writer{ESWriter: NewESWriter(client), request: request}, nil
}

func (w *ESv8Writer) Write(ctx context.Context, index string, records Records) (*Response, error) {
	for _, record := range records {
		// ES8 已移除 mapping type，批量请求的元数据也不能包含 _type。
		delete(record.Meta, "_type")
	}
	body, err := w.getBodyByRecords(records)
	if err != nil {
		return nil, err
	}
	defer bufferpool.Put(body)

	request := w.request
	request.Index = index
	request.Body = body
	response, err := request.Do(ctx, w.transport)
	if err != nil {
		return nil, err
	}
	return &Response{StatusCode: response.StatusCode, Header: response.Header, Body: response.Body}, nil
}

func init() {
	RegisterBulkWriter("v8", NewESv8Writer)
}
