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
	"testing"

	es5 "github.com/elastic/go-elasticsearch/v5"
	es6 "github.com/elastic/go-elasticsearch/v6"
	es7 "github.com/elastic/go-elasticsearch/v7"
	"github.com/stretchr/testify/assert"
)

// 测试获取不同版本的es客户端
func TestNewElasticsearch(t *testing.T) {
	es, err := NewElasticsearch("5", []string{"http://127.0.0.1:9200"}, "elastic", "123456")
	assert.Nil(t, err)
	assert.NotNil(t, es)
	client5 := es.client.(*es5.Client)
	assert.NotNil(t, client5)

	es, err = NewElasticsearch("6", []string{"http://127.0.0.1:9200"}, "elastic", "123456")
	assert.Nil(t, err)
	assert.NotNil(t, es)
	client6 := es.client.(*es6.Client)
	assert.NotNil(t, client6)

	// 其他版本都使用7版本的客户端
	es, err = NewElasticsearch("7", []string{"http://127.0.0.1:9200"}, "elastic", "123456")
	assert.Nil(t, err)
	assert.NotNil(t, es)
	client7 := es.client.(*es7.Client)
	assert.NotNil(t, client7)

	es, err = NewElasticsearch("8", []string{"http://127.0.0.1:9200"}, "elastic", "123456")
	assert.Nil(t, err)
	assert.NotNil(t, es)
	client8 := es.client.(*es7.Client)
	assert.NotNil(t, client8)
}
