// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build elasticsearch_v9

package elasticsearch

import (
	"context"

	"github.com/elastic/go-elasticsearch/v9"
	"github.com/elastic/go-elasticsearch/v9/esapi"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/bufferpool"
)

type ESv9Writer struct {
	*ESWriter
	request esapi.BulkRequest
}

func NewESv9Writer(config map[string]interface{}) (BulkWriter, error) {
	var request esapi.BulkRequest
	if err := ApplyFields(&request, config); err != nil {
		return nil, err
	}
	var clientConfig elasticsearch.Config
	if err := ApplyFields(&clientConfig, config); err != nil {
		return nil, err
	}
	// SDK 默认会对 502/503/504 进行请求级重试；这里关闭它，
	// 统一由 BulkHandler 按逐条回执重试，避免两层重试叠加。
	// https://www.elastic.co/docs/reference/elasticsearch/clients/go/configuration#_config_retry
	clientConfig.DisableRetry = true
	client, err := elasticsearch.NewClient(clientConfig)
	if err != nil {
		return nil, err
	}
	return &ESv9Writer{ESWriter: NewESWriter(client), request: request}, nil
}

func (w *ESv9Writer) Write(ctx context.Context, index string, records Records) (*Response, error) {
	for _, record := range records {
		// ES9 沿用自 8.0 起的无类型 API，批量元数据保留索引和文档 ID，移除 _type。
		// https://www.elastic.co/guide/en/elasticsearch/reference/8.19/migrating-8.0.html#remove-mapping-type-api-endpoints
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
	RegisterBulkWriter("v9", NewESv9Writer)
}
