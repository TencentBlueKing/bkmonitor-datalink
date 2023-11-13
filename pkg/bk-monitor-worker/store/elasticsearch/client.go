// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearch

import (
	"context"
	"fmt"
	"io"
	"strings"

	es5 "github.com/elastic/go-elasticsearch/v5"
	esapi5 "github.com/elastic/go-elasticsearch/v5/esapi"
	es6 "github.com/elastic/go-elasticsearch/v6"
	esapi6 "github.com/elastic/go-elasticsearch/v6/esapi"
	es7 "github.com/elastic/go-elasticsearch/v7"
	esapi7 "github.com/elastic/go-elasticsearch/v7/esapi"
	"github.com/pkg/errors"
)

type Elasticsearch struct {
	client  interface{}
	Version string
}

// NewElasticsearch 根据es版本获取客户端
// address []string{"http://127.0.0.1:9200"}
// username "elastic"
// password "pwd" 明文密码
func NewElasticsearch(version string, address []string, username string, password string) (*Elasticsearch, error) {
	switch version {
	case "5":
		client, err := es5.NewClient(es5.Config{
			Addresses: address,
			Username:  username,
			Password:  password,
			Transport: nil,
		})
		if err != nil {
			return nil, err
		}
		return &Elasticsearch{client, version}, nil
	case "6":
		client, err := es6.NewClient(es6.Config{
			Addresses: address,
			Username:  username,
			Password:  password,
			Transport: nil,
		})
		if err != nil {
			return nil, err
		}
		return &Elasticsearch{client, version}, nil
	default:
		client, err := es7.NewClient(es7.Config{
			Addresses: address,
			Username:  username,
			Password:  password,
			Transport: nil,
		})
		if err != nil {
			return nil, err
		}
		return &Elasticsearch{client, version}, nil
	}

}

// Ping 验证ES客户端连接
func (e Elasticsearch) Ping(ctx context.Context) (*Response, error) {
	switch e.Version {
	case "5":
		client, ok := e.client.(*es5.Client)
		if !ok {
			return nil, errors.Errorf("es client version error")
		}
		response, err := client.Ping(client.Ping.WithContext(ctx))
		if err != nil {
			return nil, err
		}
		resp := e.ParseResponse(response)
		return resp, nil
	case "6":
		client, ok := e.client.(*es6.Client)
		if !ok {
			return nil, errors.Errorf("es client version error")
		}
		response, err := client.Ping(client.Ping.WithContext(ctx))
		if err != nil {
			return nil, err
		}
		resp := &Response{
			StatusCode: response.StatusCode,
			Header:     response.Header,
			Body:       response.Body,
		}
		return resp, err
	default:
		client, ok := e.client.(*es7.Client)
		if !ok {
			return nil, errors.Errorf("es client version error")
		}
		response, err := client.Ping(client.Ping.WithContext(ctx))
		if err != nil {
			return nil, err
		}
		resp := &Response{
			StatusCode: response.StatusCode,
			Header:     response.Header,
			Body:       response.Body,
		}
		return resp, err
	}
}

// ParseResponse 对ES查询返回值进行封装
func (e Elasticsearch) ParseResponse(resp interface{}) *Response {
	switch e.Version {
	case "5":
		response, _ := resp.(*esapi5.Response)
		return &Response{
			StatusCode: response.StatusCode,
			Header:     response.Header,
			Body:       response.Body,
		}
	case "6":
		response, _ := resp.(*esapi6.Response)
		return &Response{
			StatusCode: response.StatusCode,
			Header:     response.Header,
			Body:       response.Body,
		}
	default:
		response, _ := resp.(*esapi7.Response)
		return &Response{
			StatusCode: response.StatusCode,
			Header:     response.Header,
			Body:       response.Body,
		}
	}
}

// GetIndices 获取索引信息
func (e Elasticsearch) GetIndices(index string) (*Response, error) {
	switch e.Version {
	case "5":
		client, _ := e.client.(*es5.Client)
		response, err := client.Indices.Get([]string{index})
		if err != nil {
			return nil, err
		}
		resp := e.ParseResponse(response)
		return resp, nil
	case "6":
		client, _ := e.client.(*es6.Client)
		response, err := client.Indices.Get([]string{index})
		if err != nil {
			return nil, err
		}
		resp := e.ParseResponse(response)
		return resp, nil
	default:
		client, _ := e.client.(*es7.Client)
		response, err := client.Indices.Get([]string{index})
		if err != nil {
			return nil, err
		}
		resp := e.ParseResponse(response)
		return resp, nil
	}
}

// SearchWithBody 通过body进行数据查询
func (e Elasticsearch) SearchWithBody(ctx context.Context, index string, body io.Reader) (*Response, error) {
	switch e.Version {
	case "5":
		client, _ := e.client.(*es5.Client)
		response, err := client.Search(
			client.Search.WithContext(ctx),
			client.Search.WithIndex(index),
			client.Search.WithBody(body),
		)
		if err != nil {
			return nil, err
		}
		resp := e.ParseResponse(response)
		return resp, nil
	case "6":
		client, _ := e.client.(*es6.Client)
		response, err := client.Search(
			client.Search.WithContext(ctx),
			client.Search.WithIndex(index),
			client.Search.WithBody(body),
		)
		if err != nil {
			return nil, err
		}
		resp := e.ParseResponse(response)
		return resp, nil
	default:
		client, _ := e.client.(*es7.Client)
		response, err := client.Search(
			client.Search.WithContext(ctx),
			client.Search.WithIndex(index),
			client.Search.WithBody(body),
		)
		if err != nil {
			return nil, err
		}
		resp := e.ParseResponse(response)
		return resp, nil
	}
}

// ComposeESHosts 拼接es链接地址，支持ipv6
func ComposeESHosts(schema string, host string, port uint) []string {
	if !strings.HasPrefix(host, "[") {
		host = "[" + host
	}
	if !strings.HasSuffix(host, "]") {
		host = host + "]"
	}
	if schema == "" {
		schema = "http"
	}
	return []string{fmt.Sprintf("%s://%s:%v", schema, host, port)}
}
