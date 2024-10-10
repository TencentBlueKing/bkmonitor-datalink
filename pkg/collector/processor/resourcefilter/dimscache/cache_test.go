// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package dimscache

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/json"
)

func TestCache(t *testing.T) {
	row1 := map[string]string{
		"key1": "val1",
		"key2": "val2",
	}
	row2 := map[string]string{
		"key1": "val3",
		"key2": "val4",
	}
	data := []map[string]string{row1, row2}

	svr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := json.Marshal(data)
		w.Write(b)
	}))

	c, err := New(&Config{
		URL: svr.URL,
		Key: "key1",
	})
	assert.NoError(t, err)
	assert.NoError(t, c.sync())

	var v map[string]string
	var ok bool

	v, ok = c.Get("val1")
	assert.True(t, ok)
	assert.Equal(t, row1, v)

	v, ok = c.Get("val2")
	assert.False(t, ok)
	assert.Empty(t, v)

	v, ok = c.Get("val3")
	assert.True(t, ok)
	assert.Equal(t, row2, v)

	c.Clean()
}
