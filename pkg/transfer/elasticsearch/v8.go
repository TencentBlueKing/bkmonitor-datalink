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

	"github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/esapi"
)

// ESv8Writer writes bulk requests with the native Elasticsearch 8 client.
type ESv8Writer struct {
	*ESWriter
	request esapi.BulkRequest
}

// NewESv8Writer creates an Elasticsearch 8 bulk writer.
func NewESv8Writer(config map[string]interface{}) (BulkWriter, error) {
	var request esapi.BulkRequest
	if err := ApplyFields(&request, config); err != nil {
		return nil, err
	}

	var c elasticsearch.Config
	if err := ApplyFields(&c, config); err != nil {
		return nil, err
	}

	client, err := elasticsearch.NewClient(c)
	if err != nil {
		return nil, err
	}

	return &ESv8Writer{
		request:  request,
		ESWriter: NewESWriter(client.Transport),
	}, nil
}

// Write sends records without mapping type metadata, which Elasticsearch 8 no longer supports.
func (w *ESv8Writer) Write(ctx context.Context, index string, records Records) (*Response, error) {
	v8Records := make(Records, 0, len(records))
	for _, record := range records {
		meta := make(map[string]interface{}, len(record.Meta))
		for key, value := range record.Meta {
			if key != "_type" {
				meta[key] = value
			}
		}
		v8Records = append(v8Records, &Record{Meta: meta, Document: record.Document})
	}

	body, err := w.getBodyByRecords(v8Records)
	if err != nil {
		return nil, err
	}

	request := w.request
	request.Body = body
	request.Index = index

	response, err := request.Do(ctx, w.transport)
	if err != nil {
		return nil, err
	}

	return &Response{
		StatusCode: response.StatusCode,
		Header:     response.Header,
		Body:       response.Body,
	}, nil
}

func init() {
	RegisterBulkWriter("v8", NewESv8Writer)
}
