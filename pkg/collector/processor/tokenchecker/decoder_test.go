// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package tokenchecker

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
)

func TestFixedDecoder(t *testing.T) {
	var (
		tracesDataId  int32 = 10010
		metricsDataId int32 = 10011
		logsDataId    int32 = 10012
	)

	decoder := newFixedTokenDecoder(Config{
		Type:          "fixed",
		TracesDataId:  tracesDataId,
		MetricsDataId: metricsDataId,
		LogsDataId:    logsDataId,
	})
	assert.Equal(t, decoderTypeFixed, decoder.Type())
	assert.True(t, decoder.Skip())

	token, err := decoder.Decode("")
	assert.NoError(t, err)
	assert.Equal(t, token.TracesDataId, tracesDataId)
	assert.Equal(t, token.MetricsDataId, metricsDataId)
	assert.Equal(t, token.LogsDataId, logsDataId)
}

func TestDefaultDecoder(t *testing.T) {
	decoder := NewTokenDecoder(Config{})
	assert.Equal(t, decoderTypeFixed, decoder.Type())
	assert.True(t, decoder.Skip())
}

var aes256TokenDecoderConfig = Config{
	Type:       "aes256",
	Salt:       "bk",
	DecodedIv:  "bkbkbkbkbkbkbkbk",
	DecodedKey: "81be7fc6-5476-4934-9417-6d4d593728db",
}

func TestAes256Decoder(t *testing.T) {
	decoder := NewTokenDecoder(aes256TokenDecoderConfig)
	assert.Equal(t, decoderTypeAcs256, decoder.Type())
	assert.False(t, decoder.Skip())

	cases := []struct {
		Input     string
		Token     define.Token
		ErrPrefix string
	}{
		{
			Input: "Ymtia2JrYmtia2JrYmtiaxUtdLzrldhHtlcjc1Cwfo1u99rVk5HGe8EjT761brGtKm3H4Ran78rWl85HwzfRgw==",
			Token: define.Token{
				Original:      "Ymtia2JrYmtia2JrYmtiaxUtdLzrldhHtlcjc1Cwfo1u99rVk5HGe8EjT761brGtKm3H4Ran78rWl85HwzfRgw==",
				TracesDataId:  1001,
				MetricsDataId: 1002,
				LogsDataId:    1003,
				BizId:         2,
				AppName:       "oneapm-appname",
			},
		},
		{
			Input: "Ymtia2JrYmtia2JrYmtia/0ZJ3tXGU6OT2oEqyruVbvWr0kNl7AzgSWPsnVzNBYWRULf8XE/mtQBHLas+jYCrw==",
			Token: define.Token{
				Original:       "Ymtia2JrYmtia2JrYmtia/0ZJ3tXGU6OT2oEqyruVbvWr0kNl7AzgSWPsnVzNBYWRULf8XE/mtQBHLas+jYCrw==",
				TracesDataId:   1001,
				MetricsDataId:  1002,
				LogsDataId:     1003,
				ProfilesDataId: 1004,
				BizId:          2,
				AppName:        "oneapm-appname",
			},
		},
		{
			Input:     "Ymtia2JrYmtia2JrYmtiaxUtdLzrldhHtlcjc1Cwfo1u99rVk5HGe8EjT761brGtKm3H4Ran78rWl85HwzfR",
			Token:     define.Token{},
			ErrPrefix: "crypto/cipher: input not full blocks",
		},
		{
			Input:     "not_base64_string",
			Token:     define.Token{},
			ErrPrefix: "illegal base64 data",
		},
		{
			Input:     "YmstY29sbGVjdG9y",
			Token:     define.Token{},
			ErrPrefix: "invalid prefix-enc",
		},
	}

	for _, c := range cases {
		token, err := decoder.Decode(c.Input)
		switch err {
		case nil:
			assert.Empty(t, c.ErrPrefix)
		default:
			assert.True(t, strings.Contains(err.Error(), c.ErrPrefix))
		}

		assert.Equal(t, c.Token, token)
	}
}

func TestProxyTokenDecoder(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		decoder := NewTokenDecoder(Config{
			Type:        decoderTypeProxy,
			ProxyDataId: 999,
			ProxyToken:  "test_proxy_token",
		})
		assert.Equal(t, decoderTypeProxy, decoder.Type())
		assert.False(t, decoder.Skip())

		token, err := decoder.Decode(define.WrapProxyToken(define.Token{
			Original:    "test_proxy_token",
			ProxyDataId: 999,
		}))
		assert.NoError(t, err)
		assert.Equal(t, int32(999), token.ProxyDataId)
		assert.Equal(t, "test_proxy_token", token.Original)
	})

	t.Run("Empty", func(t *testing.T) {
		decoder := NewTokenDecoder(Config{
			Type:        decoderTypeProxy,
			ProxyDataId: 999,
			ProxyToken:  "test_proxy_token",
		})

		token, err := decoder.Decode(define.WrapProxyToken(define.Token{}))
		assert.Equal(t, "reject empty token", err.Error())
		assert.Equal(t, int32(0), token.ProxyDataId)
		assert.Equal(t, "", token.Original)
	})

	t.Run("Invalid", func(t *testing.T) {
		decoder := NewTokenDecoder(Config{
			Type:        decoderTypeProxy,
			ProxyDataId: 999,
			ProxyToken:  "test_proxy_token",
		})

		token, err := decoder.Decode(define.WrapProxyToken(define.Token{
			Original:    "invalid",
			ProxyDataId: 999,
		}))
		assert.Equal(t, "reject invalid token: 999/invalid", err.Error())
		assert.Equal(t, int32(0), token.ProxyDataId)
		assert.Equal(t, "", token.Original)
	})
}

func BenchmarkAes256Decoder(b *testing.B) {
	decoder := newAes256TokenDecoder(aes256TokenDecoderConfig)
	enc := "Ymtia2JrYmtia2JrYmtia8wN6fmFR+AoSEiL2XaAc4D4OOfEBkj4JFjaiyyPod5+rX6vWlJiypZkcxxwdHzQsw=="
	for i := 0; i < b.N; i++ {
		_, err := decoder.decode(enc)
		if err != nil {
			panic(err)
		}
	}
}

func BenchmarkAes256DecoderWithCached(b *testing.B) {
	decoder := newAes256TokenDecoder(aes256TokenDecoderConfig)
	enc := "Ymtia2JrYmtia2JrYmtia8wN6fmFR+AoSEiL2XaAc4D4OOfEBkj4JFjaiyyPod5+rX6vWlJiypZkcxxwdHzQsw=="
	for i := 0; i < b.N; i++ {
		_, err := decoder.Decode(enc)
		if err != nil {
			panic(err)
		}
	}
}
