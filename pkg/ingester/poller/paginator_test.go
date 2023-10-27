// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package poller_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/poller"
)

func TestLimitOffsetPaginator(t *testing.T) {
	paginator := poller.LimitOffsetPaginator{}
	paginator.PageSize = 10

	paginator.Reset()
	context := paginator.GetAndNext()

	assert.Equal(t, context, poller.Context{
		"limit":  "10",
		"offset": "0",
	})

	assert.False(t, paginator.HasNext())

	paginator.SetTotal(15)

	assert.True(t, paginator.HasNext())

	context = paginator.GetAndNext()

	assert.Equal(t, context, poller.Context{
		"limit":  "10",
		"offset": "10",
	})

	assert.False(t, paginator.HasNext())

	paginator.Reset()

	paginator.SetTotal(10)

	context = paginator.GetAndNext()

	assert.Equal(t, context, poller.Context{
		"limit":  "10",
		"offset": "0",
	})

	assert.False(t, paginator.HasNext())
}

func TestPageNumberPaginator(t *testing.T) {
	paginator := poller.PageNumberPaginator{}
	paginator.PageSize = 10

	paginator.Reset()

	context := paginator.GetAndNext()

	assert.Equal(t, context, poller.Context{
		"page_size": "10",
		"page":      "1",
	})

	assert.False(t, paginator.HasNext())

	paginator.SetTotal(15)

	assert.True(t, paginator.HasNext())

	context = paginator.GetAndNext()

	assert.Equal(t, context, poller.Context{
		"page_size": "10",
		"page":      "2",
	})

	assert.False(t, paginator.HasNext())

	paginator.Reset()
	paginator.SetTotal(10)

	context = paginator.GetAndNext()

	assert.Equal(t, context, poller.Context{
		"page_size": "10",
		"page":      "1",
	})

	assert.False(t, paginator.HasNext())
}

func TestNilPaginator(t *testing.T) {
	paginator := poller.NilPaginator{}

	paginator.Reset()

	context := paginator.GetAndNext()

	assert.Equal(t, context, poller.Context{})

	assert.False(t, paginator.HasNext())
}
