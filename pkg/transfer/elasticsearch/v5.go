// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build elasticsearch_v5
// +build elasticsearch_v5

package elasticsearch

import (
	"context"

	"github.com/elastic/go-elasticsearch/v5"
	"github.com/elastic/go-elasticsearch/v5/esapi"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/bufferpool"
)

// ES5Adapter
type ESv5Writer struct {
	*ESWriter
	request esapi.BulkRequest
}

// NewESv5Writer
func NewESv5Writer(config map[string]interface{}) (BulkWriter, error) {
	var request esapi.BulkRequest
	err := ApplyFields(&request, config)
	if err != nil {
		return nil, err
	}

	var c elasticsearch.Config
	err = ApplyFields(&c, config)
	if err != nil {
		return nil, err
	}

	client, err := elasticsearch.NewClient(c)
	if err != nil {
		return nil, err
	}

	return &ESv5Writer{
		request:  request,
		ESWriter: NewESWriter(client.Transport),
	}, nil
}

// Write
func (w *ESv5Writer) Write(ctx context.Context, index string, records Records) (*Response, error) {
	body, err := w.getBodyByRecords(records)
	if err != nil {
		return nil, err
	}
	defer bufferpool.Put(body)

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
	RegisterBulkWriter("v5", NewESv5Writer)
}
