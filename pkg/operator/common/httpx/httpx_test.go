// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package httpx

import (
	"encoding/base64"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWindParams(t *testing.T) {
	cases := []struct {
		Input  map[string]string
		Output string
		Base64 string
	}{
		{
			Input: map[string]string{
				"key1": "value1",
				"key2": "value2",
			},
			Output: "key1=value1&key2=value2",
			Base64: "a2V5MT12YWx1ZTEma2V5Mj12YWx1ZTI",
		},
		{
			Input: map[string]string{
				"key1": "value1",
			},
			Output: "key1=value1",
			Base64: "a2V5MT12YWx1ZTE",
		},
		{
			Input: map[string]string{
				"key1": "",
				"key2": "value2",
			},
			Output: "key2=value2",
			Base64: "a2V5Mj12YWx1ZTI",
		},
	}

	for _, c := range cases {
		assert.Equal(t, c.Base64, WindParams(c.Input))

		b, err := base64.RawURLEncoding.DecodeString(c.Base64)
		assert.NoError(t, err)
		assert.Equal(t, string(b), c.Output)
	}
}

func TestUnwindParams(t *testing.T) {
	cases := []struct {
		Input  string
		Output url.Values
	}{
		{
			Input: "a2V5MT12YWx1ZTEma2V5Mj12YWx1ZTI",
			Output: url.Values{
				"key1": []string{"value1"},
				"key2": []string{"value2"},
			},
		},
		{
			Input: "a2V5MT12YWx1ZTE",
			Output: url.Values{
				"key1": []string{"value1"},
			},
		},
	}

	for _, c := range cases {
		assert.Equal(t, c.Output, UnwindParams(c.Input))
	}
}
