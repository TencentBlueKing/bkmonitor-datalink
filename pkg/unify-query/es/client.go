// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package es

import (
	"context"
	"io"
	"strings"

	elasticsearch "github.com/elastic/go-elasticsearch/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

type Client interface {
	Search(body string, indexNames ...string) (string, error)
	Aliases() (string, error)
	AliasWithIndex(index string) (string, error)
	Indices() (string, error)
}

// ESClient es 查询 client
type ESClient struct {
	// 控制并发数
	tokenChan chan int
	client    *elasticsearch.Client
}

var NewClient = func(info *ESInfo) (Client, error) {
	client, err := elasticsearch.NewClient(
		elasticsearch.Config{
			Addresses: []string{info.Host},
			Username:  info.Username,
			Password:  info.Password,
		},
	)
	if info.MaxConcurrency == 0 {
		log.Debugf(context.TODO(), "max concurrency not set, use:%d as default", 200)
		info.MaxConcurrency = 200
	}
	return &ESClient{
		tokenChan: make(chan int, info.MaxConcurrency),
		client:    client,
	}, err
}

// Search es 接口 _search 代理
func (c *ESClient) Search(body string, indexNames ...string) (string, error) {
	c.tokenChan <- 1
	defer func() {
		<-c.tokenChan
	}()
	es := c.client
	result, err := es.Search(es.Search.WithIndex(indexNames...), es.Search.WithBody(strings.NewReader(body)))
	if err != nil {
		return "", metadata.Sprintf(
			metadata.MsgQueryES,
			"查询失败",
		).Error(context.TODO(), err)
	}
	res, err := io.ReadAll(result.Body)
	log.Debugf(context.TODO(), "search index:%v,body:%s get result:%s", indexNames, body, res)
	return string(res), err
}

// Aliases 获取 alias
func (c *ESClient) Aliases() (string, error) {
	c.tokenChan <- 1
	defer func() {
		<-c.tokenChan
	}()
	es := c.client
	result, err := es.Cat.Aliases()
	if err != nil {
		return "", metadata.Sprintf(
			metadata.MsgQueryES,
			"查询失败",
		).Error(context.TODO(), err)
	}
	res, err := io.ReadAll(result.Body)
	log.Debugf(context.TODO(), "cat aliases get result:%s", res)
	return string(res), err
}

// AliasWithIndex 通过 index 获取 alias
func (c *ESClient) AliasWithIndex(index string) (string, error) {
	c.tokenChan <- 1
	defer func() {
		<-c.tokenChan
	}()
	es := c.client
	result, err := es.Indices.GetAlias(es.Indices.GetAlias.WithIndex(index))
	if err != nil {
		return "", metadata.Sprintf(
			metadata.MsgQueryES,
			"查询失败",
		).Error(context.TODO(), err)
	}
	res, err := io.ReadAll(result.Body)
	log.Debugf(context.TODO(), "cat aliases with index:%s, get result:%s", index, res)
	return string(res), err
}

// Indices 获取 indices 信息
func (c *ESClient) Indices() (string, error) {
	c.tokenChan <- 1
	defer func() {
		<-c.tokenChan
	}()
	es := c.client
	result, err := es.API.Cat.Indices()
	if err != nil {
		return "", metadata.Sprintf(
			metadata.MsgQueryES,
			"查询失败",
		).Error(context.TODO(), err)
	}
	res, err := io.ReadAll(result.Body)
	log.Debugf(context.TODO(), "cat indices get result:%s", res)
	return string(res), err
}
