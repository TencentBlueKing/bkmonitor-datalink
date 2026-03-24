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
)

type Elasticsearch struct {
	client  any
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
			return nil, ClientVersionErr
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
	case "6":
		client, ok := e.client.(*es6.Client)
		if !ok {
			return nil, ClientVersionErr
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
			return nil, ClientVersionErr
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
func (e Elasticsearch) ParseResponse(resp any) (*Response, error) {
	switch e.Version {
	case "5":
		response, ok := resp.(*esapi5.Response)
		if !ok {
			return nil, ResponseVersionErr
		}
		wrapResp := &Response{
			StatusCode: response.StatusCode,
			Header:     response.Header,
			Body:       response.Body,
		}
		err := wrapResp.DealStatusCodeError()
		if err != nil {
			return nil, err
		}
		return wrapResp, nil
	case "6":
		response, ok := resp.(*esapi6.Response)
		if !ok {
			return nil, ResponseVersionErr
		}
		wrapResp := &Response{
			StatusCode: response.StatusCode,
			Header:     response.Header,
			Body:       response.Body,
		}
		err := wrapResp.DealStatusCodeError()
		if err != nil {
			return nil, err
		}
		return wrapResp, nil
	default:
		response, ok := resp.(*esapi7.Response)
		if !ok {
			return nil, ResponseVersionErr
		}
		wrapResp := &Response{
			StatusCode: response.StatusCode,
			Header:     response.Header,
			Body:       response.Body,
		}
		err := wrapResp.DealStatusCodeError()
		if err != nil {
			return nil, err
		}
		return wrapResp, nil
	}
}

// GetIndices 获取索引信息
func (e Elasticsearch) GetIndices(indices []string) (*Response, error) {
	switch e.Version {
	case "5":
		client, ok := e.client.(*es5.Client)
		if !ok {
			return nil, ClientVersionErr
		}
		response, err := client.Indices.Get(indices)
		if err != nil {
			return nil, err
		}
		resp, err := e.ParseResponse(response)
		if err != nil {
			return nil, err
		}
		return resp, nil
	case "6":
		client, ok := e.client.(*es6.Client)
		if !ok {
			return nil, ClientVersionErr
		}
		response, err := client.Indices.Get(indices)
		if err != nil {
			return nil, err
		}
		resp, err := e.ParseResponse(response)
		if err != nil {
			return nil, err
		}
		return resp, nil
	default:
		client, ok := e.client.(*es7.Client)
		if !ok {
			return nil, ClientVersionErr
		}
		response, err := client.Indices.Get(indices)
		if err != nil {
			return nil, err
		}
		resp, err := e.ParseResponse(response)
		if err != nil {
			return nil, err
		}
		return resp, nil
	}
}

// CatIndices 获取索引统计数据
func (e Elasticsearch) CatIndices(ctx context.Context, indices []string, format string) (*Response, error) {
	switch e.Version {
	case "5":
		client, ok := e.client.(*es5.Client)
		if !ok {
			return nil, ClientVersionErr
		}
		response, err := client.Cat.Indices(client.Cat.Indices.WithIndex(indices...), client.Cat.Indices.WithFormat(format), client.Cat.Indices.WithContext(ctx))
		if err != nil {
			return nil, err
		}
		resp, err := e.ParseResponse(response)
		if err != nil {
			return nil, err
		}
		return resp, nil
	case "6":
		client, ok := e.client.(*es6.Client)
		if !ok {
			return nil, ClientVersionErr
		}
		response, err := client.Cat.Indices(client.Cat.Indices.WithIndex(indices...), client.Cat.Indices.WithFormat(format), client.Cat.Indices.WithContext(ctx))
		if err != nil {
			return nil, err
		}
		resp, err := e.ParseResponse(response)
		if err != nil {
			return nil, err
		}
		return resp, nil
	default:
		client, ok := e.client.(*es7.Client)
		if !ok {
			return nil, ClientVersionErr
		}
		response, err := client.Cat.Indices(client.Cat.Indices.WithIndex(indices...), client.Cat.Indices.WithFormat(format), client.Cat.Indices.WithContext(ctx))
		if err != nil {
			return nil, err
		}
		resp, err := e.ParseResponse(response)
		if err != nil {
			return nil, err
		}
		return resp, nil
	}
}

// SearchWithBody 通过body进行数据查询
func (e Elasticsearch) SearchWithBody(ctx context.Context, index string, body io.Reader) (*Response, error) {
	switch e.Version {
	case "5":
		client, ok := e.client.(*es5.Client)
		if !ok {
			return nil, ClientVersionErr
		}
		response, err := client.Search(
			client.Search.WithContext(ctx),
			client.Search.WithIndex(index),
			client.Search.WithBody(body),
		)
		if err != nil {
			return nil, err
		}
		resp, err := e.ParseResponse(response)
		if err != nil {
			return nil, err
		}
		return resp, nil
	case "6":
		client, ok := e.client.(*es6.Client)
		if !ok {
			return nil, ClientVersionErr
		}
		response, err := client.Search(
			client.Search.WithContext(ctx),
			client.Search.WithIndex(index),
			client.Search.WithBody(body),
		)
		if err != nil {
			return nil, err
		}
		resp, err := e.ParseResponse(response)
		if err != nil {
			return nil, err
		}
		return resp, nil
	default:
		client, ok := e.client.(*es7.Client)
		if !ok {
			return nil, ClientVersionErr
		}
		response, err := client.Search(
			client.Search.WithContext(ctx),
			client.Search.WithIndex(index),
			client.Search.WithBody(body),
		)
		if err != nil {
			return nil, err
		}
		resp, err := e.ParseResponse(response)
		if err != nil {
			return nil, err
		}
		return resp, nil
	}
}

// CreateIndex 创建索引
func (e Elasticsearch) CreateIndex(ctx context.Context, index string, body io.Reader) (*Response, error) {
	switch e.Version {
	case "5":
		client, ok := e.client.(*es5.Client)
		if !ok {
			return nil, ClientVersionErr
		}
		response, err := client.Indices.Create(
			index,
			client.Indices.Create.WithBody(body),
			client.Indices.Create.WithContext(ctx),
		)
		if err != nil {
			return nil, err
		}
		resp, err := e.ParseResponse(response)
		if err != nil {
			return nil, err
		}
		return resp, nil
	case "6":
		client, ok := e.client.(*es6.Client)
		if !ok {
			return nil, ClientVersionErr
		}
		response, err := client.Indices.Create(
			index,
			client.Indices.Create.WithBody(body),
			client.Indices.Create.WithContext(ctx),
		)
		if err != nil {
			return nil, err
		}
		resp, err := e.ParseResponse(response)
		if err != nil {
			return nil, err
		}
		return resp, nil
	default:
		client, ok := e.client.(*es7.Client)
		if !ok {
			return nil, ClientVersionErr
		}
		response, err := client.Indices.Create(
			index,
			client.Indices.Create.WithBody(body),
			client.Indices.Create.WithContext(ctx),
		)
		if err != nil {
			return nil, err
		}
		resp, err := e.ParseResponse(response)
		if err != nil {
			return nil, err
		}
		return resp, nil
	}
}

// IndexStat 获取所以状态信息
func (e Elasticsearch) IndexStat(ctx context.Context, index string, metrics []string) (*Response, error) {
	switch e.Version {
	case "5":
		client, ok := e.client.(*es5.Client)
		if !ok {
			return nil, ClientVersionErr
		}
		response, err := client.Indices.Stats(
			client.Indices.Stats.WithIndex(index),
			client.Indices.Stats.WithMetric(metrics...),
			client.Indices.Stats.WithContext(ctx),
		)
		if err != nil {
			return nil, err
		}
		resp, err := e.ParseResponse(response)
		if err != nil {
			return nil, err
		}
		return resp, nil
	case "6":
		client, ok := e.client.(*es6.Client)
		if !ok {
			return nil, ClientVersionErr
		}
		response, err := client.Indices.Stats(
			client.Indices.Stats.WithIndex(index),
			client.Indices.Stats.WithMetric(metrics...),
			client.Indices.Stats.WithContext(ctx),
		)
		if err != nil {
			return nil, err
		}
		resp, err := e.ParseResponse(response)
		if err != nil {
			return nil, err
		}
		return resp, nil
	default:
		client, ok := e.client.(*es7.Client)
		if !ok {
			return nil, ClientVersionErr
		}
		response, err := client.Indices.Stats(
			client.Indices.Stats.WithIndex(index),
			client.Indices.Stats.WithMetric(metrics...),
			client.Indices.Stats.WithContext(ctx),
		)
		if err != nil {
			return nil, err
		}
		resp, err := e.ParseResponse(response)
		if err != nil {
			return nil, err
		}
		return resp, nil
	}
}

// GetAlias 获取别名信息
func (e Elasticsearch) GetAlias(ctx context.Context, index string) (*Response, error) {
	switch e.Version {
	case "5":
		client, ok := e.client.(*es5.Client)
		if !ok {
			return nil, ClientVersionErr
		}
		response, err := client.Indices.GetAlias(
			client.Indices.GetAlias.WithIndex(index),
			client.Indices.GetAlias.WithContext(ctx),
		)
		if err != nil {
			return nil, err
		}
		resp, err := e.ParseResponse(response)
		if err != nil {
			return nil, err
		}
		return resp, nil
	case "6":
		client, ok := e.client.(*es6.Client)
		if !ok {
			return nil, ClientVersionErr
		}
		response, err := client.Indices.GetAlias(
			client.Indices.GetAlias.WithIndex(index),
			client.Indices.GetAlias.WithContext(ctx),
		)
		if err != nil {
			return nil, err
		}
		resp, err := e.ParseResponse(response)
		if err != nil {
			return nil, err
		}
		return resp, nil
	default:
		client, ok := e.client.(*es7.Client)
		if !ok {
			return nil, ClientVersionErr
		}
		response, err := client.Indices.GetAlias(
			client.Indices.GetAlias.WithIndex(index),
			client.Indices.GetAlias.WithContext(ctx),
		)
		if err != nil {
			return nil, err
		}
		resp, err := e.ParseResponse(response)
		if err != nil {
			return nil, err
		}
		return resp, nil
	}
}

// UpdateAlias 更新别名
func (e Elasticsearch) UpdateAlias(ctx context.Context, body io.Reader) (*Response, error) {
	switch e.Version {
	case "5":
		client, ok := e.client.(*es5.Client)
		if !ok {
			return nil, ClientVersionErr
		}
		response, err := client.Indices.UpdateAliases(body, client.Indices.UpdateAliases.WithContext(ctx))
		if err != nil {
			return nil, err
		}
		resp, err := e.ParseResponse(response)
		if err != nil {
			return nil, err
		}
		return resp, nil
	case "6":
		client, ok := e.client.(*es6.Client)
		if !ok {
			return nil, ClientVersionErr
		}
		response, err := client.Indices.UpdateAliases(body, client.Indices.UpdateAliases.WithContext(ctx))
		if err != nil {
			return nil, err
		}
		resp, err := e.ParseResponse(response)
		if err != nil {
			return nil, err
		}
		return resp, nil
	default:
		client, ok := e.client.(*es7.Client)
		if !ok {
			return nil, ClientVersionErr
		}
		response, err := client.Indices.UpdateAliases(body, client.Indices.UpdateAliases.WithContext(ctx))
		if err != nil {
			return nil, err
		}
		resp, err := e.ParseResponse(response)
		if err != nil {
			return nil, err
		}
		return resp, nil
	}
}

// DeleteAlias 删除别名
func (e Elasticsearch) DeleteAlias(ctx context.Context, indices, alias []string) (*Response, error) {
	switch e.Version {
	case "5":
		client, ok := e.client.(*es5.Client)
		if !ok {
			return nil, ClientVersionErr
		}
		response, err := client.Indices.DeleteAlias(indices, alias, client.Indices.DeleteAlias.WithContext(ctx))
		if err != nil {
			return nil, err
		}
		resp, err := e.ParseResponse(response)
		if err != nil {
			return nil, err
		}
		return resp, nil
	case "6":
		client, ok := e.client.(*es6.Client)
		if !ok {
			return nil, ClientVersionErr
		}
		response, err := client.Indices.DeleteAlias(indices, alias, client.Indices.DeleteAlias.WithContext(ctx))
		if err != nil {
			return nil, err
		}
		resp, err := e.ParseResponse(response)
		if err != nil {
			return nil, err
		}
		return resp, nil
	default:
		client, ok := e.client.(*es7.Client)
		if !ok {
			return nil, ClientVersionErr
		}
		response, err := client.Indices.DeleteAlias(indices, alias, client.Indices.DeleteAlias.WithContext(ctx))
		if err != nil {
			return nil, err
		}
		resp, err := e.ParseResponse(response)
		if err != nil {
			return nil, err
		}
		return resp, nil
	}
}

// DeleteIndex 删除索引
func (e Elasticsearch) DeleteIndex(ctx context.Context, indices []string) (*Response, error) {
	switch e.Version {
	case "5":
		client, ok := e.client.(*es5.Client)
		if !ok {
			return nil, ClientVersionErr
		}
		response, err := client.Indices.Delete(indices, client.Indices.Delete.WithContext(ctx))
		if err != nil {
			return nil, err
		}
		resp, err := e.ParseResponse(response)
		if err != nil {
			return nil, err
		}
		return resp, nil
	case "6":
		client, ok := e.client.(*es6.Client)
		if !ok {
			return nil, ClientVersionErr
		}
		response, err := client.Indices.Delete(indices, client.Indices.Delete.WithContext(ctx), client.Indices.Delete.WithIgnoreUnavailable(true))
		if err != nil {
			return nil, err
		}
		resp, err := e.ParseResponse(response)
		if err != nil {
			return nil, err
		}
		return resp, nil
	default:
		client, ok := e.client.(*es7.Client)
		if !ok {
			return nil, ClientVersionErr
		}
		response, err := client.Indices.Delete(indices, client.Indices.Delete.WithContext(ctx), client.Indices.Delete.WithIgnoreUnavailable(true))
		if err != nil {
			return nil, err
		}
		resp, err := e.ParseResponse(response)
		if err != nil {
			return nil, err
		}
		return resp, nil
	}
}

// CountByIndex 统计索引中的数据
func (e Elasticsearch) CountByIndex(ctx context.Context, indices []string) (*Response, error) {
	switch e.Version {
	case "5":
		client, ok := e.client.(*es5.Client)
		if !ok {
			return nil, ClientVersionErr
		}
		response, err := client.Count(client.Count.WithIndex(indices...), client.Count.WithContext(ctx))
		if err != nil {
			return nil, err
		}
		resp, err := e.ParseResponse(response)
		if err != nil {
			return nil, err
		}
		return resp, nil
	case "6":
		client, ok := e.client.(*es6.Client)
		if !ok {
			return nil, ClientVersionErr
		}
		response, err := client.Count(client.Count.WithIndex(indices...), client.Count.WithContext(ctx))
		if err != nil {
			return nil, err
		}
		resp, err := e.ParseResponse(response)
		if err != nil {
			return nil, err
		}
		return resp, nil
	default:
		client, ok := e.client.(*es7.Client)
		if !ok {
			return nil, ClientVersionErr
		}
		response, err := client.Count(client.Count.WithIndex(indices...), client.Count.WithContext(ctx))
		if err != nil {
			return nil, err
		}
		resp, err := e.ParseResponse(response)
		if err != nil {
			return nil, err
		}
		return resp, nil
	}
}

// GetIndexMapping 获取索引的mapping
func (e Elasticsearch) GetIndexMapping(ctx context.Context, indices []string) (*Response, error) {
	switch e.Version {
	case "5":
		client, ok := e.client.(*es5.Client)
		if !ok {
			return nil, ClientVersionErr
		}
		response, err := client.Indices.GetMapping(client.Indices.GetMapping.WithIndex(indices...), client.Indices.GetMapping.WithContext(ctx))
		if err != nil {
			return nil, err
		}
		resp, err := e.ParseResponse(response)
		if err != nil {
			return nil, err
		}
		return resp, nil
	case "6":
		client, ok := e.client.(*es6.Client)
		if !ok {
			return nil, ClientVersionErr
		}
		response, err := client.Indices.GetMapping(client.Indices.GetMapping.WithIndex(indices...), client.Indices.GetMapping.WithContext(ctx))
		if err != nil {
			return nil, err
		}
		resp, err := e.ParseResponse(response)
		if err != nil {
			return nil, err
		}
		return resp, nil
	default:
		client, ok := e.client.(*es7.Client)
		if !ok {
			return nil, ClientVersionErr
		}
		response, err := client.Indices.GetMapping(client.Indices.GetMapping.WithIndex(indices...), client.Indices.GetMapping.WithContext(ctx))
		if err != nil {
			return nil, err
		}
		resp, err := e.ParseResponse(response)
		if err != nil {
			return nil, err
		}
		return resp, nil
	}
}

// PutSettings 修改索引的mapping
func (e Elasticsearch) PutSettings(ctx context.Context, body io.Reader, indices []string) (*Response, error) {
	switch e.Version {
	case "5":
		client, ok := e.client.(*es5.Client)
		if !ok {
			return nil, ClientVersionErr
		}
		response, err := client.Indices.PutSettings(body, client.Indices.PutSettings.WithIndex(indices...), client.Indices.PutSettings.WithContext(ctx))
		if err != nil {
			return nil, err
		}
		resp, err := e.ParseResponse(response)
		if err != nil {
			return nil, err
		}
		return resp, nil
	case "6":
		client, ok := e.client.(*es6.Client)
		if !ok {
			return nil, ClientVersionErr
		}
		response, err := client.Indices.PutSettings(body, client.Indices.PutSettings.WithIndex(indices...), client.Indices.PutSettings.WithContext(ctx))
		if err != nil {
			return nil, err
		}
		resp, err := e.ParseResponse(response)
		if err != nil {
			return nil, err
		}
		return resp, nil
	default:
		client, ok := e.client.(*es7.Client)
		if !ok {
			return nil, ClientVersionErr
		}
		response, err := client.Indices.PutSettings(body, client.Indices.PutSettings.WithIndex(indices...), client.Indices.PutSettings.WithContext(ctx))
		if err != nil {
			return nil, err
		}
		resp, err := e.ParseResponse(response)
		if err != nil {
			return nil, err
		}
		return resp, nil
	}
}

// GetSnapshot 获取快照信息
func (e Elasticsearch) GetSnapshot(ctx context.Context, repository string, snapshot []string) (*Response, error) {
	switch e.Version {
	case "5":
		client, ok := e.client.(*es5.Client)
		if !ok {
			return nil, ClientVersionErr
		}
		response, err := client.Snapshot.Get(repository, snapshot, client.Snapshot.Get.WithContext(ctx))
		if err != nil {
			return nil, err
		}
		resp, err := e.ParseResponse(response)
		if err != nil {
			return nil, err
		}
		return resp, nil
	case "6":
		client, ok := e.client.(*es6.Client)
		if !ok {
			return nil, ClientVersionErr
		}
		response, err := client.Snapshot.Get(repository, snapshot, client.Snapshot.Get.WithContext(ctx))
		if err != nil {
			return nil, err
		}
		resp, err := e.ParseResponse(response)
		if err != nil {
			return nil, err
		}
		return resp, nil
	default:
		client, ok := e.client.(*es7.Client)
		if !ok {
			return nil, ClientVersionErr
		}
		response, err := client.Snapshot.Get(repository, snapshot, client.Snapshot.Get.WithContext(ctx))
		if err != nil {
			return nil, err
		}
		resp, err := e.ParseResponse(response)
		if err != nil {
			return nil, err
		}
		return resp, nil
	}
}

// CreateSnapshot 创建快照
func (e Elasticsearch) CreateSnapshot(ctx context.Context, repository string, snapshot string, body io.Reader) (*Response, error) {
	switch e.Version {
	case "5":
		client, ok := e.client.(*es5.Client)
		if !ok {
			return nil, ClientVersionErr
		}
		response, err := client.Snapshot.Create(
			repository,
			snapshot,
			client.Snapshot.Create.WithBody(body),
			client.Snapshot.Create.WithContext(ctx),
		)
		if err != nil {
			return nil, err
		}
		resp, err := e.ParseResponse(response)
		if err != nil {
			return nil, err
		}
		return resp, nil
	case "6":
		client, ok := e.client.(*es6.Client)
		if !ok {
			return nil, ClientVersionErr
		}
		response, err := client.Snapshot.Create(
			repository,
			snapshot,
			client.Snapshot.Create.WithBody(body),
			client.Snapshot.Create.WithContext(ctx),
		)
		if err != nil {
			return nil, err
		}
		resp, err := e.ParseResponse(response)
		if err != nil {
			return nil, err
		}
		return resp, nil
	default:
		client, ok := e.client.(*es7.Client)
		if !ok {
			return nil, ClientVersionErr
		}
		response, err := client.Snapshot.Create(
			repository,
			snapshot,
			client.Snapshot.Create.WithBody(body),
			client.Snapshot.Create.WithContext(ctx),
		)
		if err != nil {
			return nil, err
		}
		resp, err := e.ParseResponse(response)
		if err != nil {
			return nil, err
		}
		return resp, nil
	}
}

// DeleteSnapshot 删除快照
func (e Elasticsearch) DeleteSnapshot(ctx context.Context, repository string, snapshot string) (*Response, error) {
	switch e.Version {
	case "5":
		client, ok := e.client.(*es5.Client)
		if !ok {
			return nil, ClientVersionErr
		}
		response, err := client.Snapshot.Delete(repository, snapshot, client.Snapshot.Delete.WithContext(ctx))
		if err != nil {
			return nil, err
		}
		resp, err := e.ParseResponse(response)
		if err != nil {
			return nil, err
		}
		return resp, nil
	case "6":
		client, ok := e.client.(*es6.Client)
		if !ok {
			return nil, ClientVersionErr
		}
		response, err := client.Snapshot.Delete(repository, snapshot, client.Snapshot.Delete.WithContext(ctx))
		if err != nil {
			return nil, err
		}
		resp, err := e.ParseResponse(response)
		if err != nil {
			return nil, err
		}
		return resp, nil
	default:
		client, ok := e.client.(*es7.Client)
		if !ok {
			return nil, ClientVersionErr
		}
		response, err := client.Snapshot.Delete(repository, snapshot, client.Snapshot.Delete.WithContext(ctx))
		if err != nil {
			return nil, err
		}
		resp, err := e.ParseResponse(response)
		if err != nil {
			return nil, err
		}
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
