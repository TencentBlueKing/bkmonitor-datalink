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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/metacache"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/tokenparser"
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
	assert.Equal(t, decoderTypeAes256, decoder.Type())
	assert.False(t, decoder.Skip())

	const (
		token1 = "Ymtia2JrYmtia2JrYmtiaxUtdLzrldhHtlcjc1Cwfo1u99rVk5HGe8EjT761brGtKm3H4Ran78rWl85HwzfRgw=="
		token2 = "Ymtia2JrYmtia2JrYmtia/0ZJ3tXGU6OT2oEqyruVbvWr0kNl7AzgSWPsnVzNBYWRULf8XE/mtQBHLas+jYCrw=="
	)

	tests := []struct {
		Input     string
		Token     define.Token
		ErrPrefix string
	}{
		{
			Input: token1,
			Token: define.Token{
				Original:      token1,
				TracesDataId:  1001,
				MetricsDataId: 1002,
				LogsDataId:    1003,
				BizId:         2,
				AppName:       "oneapm-appname",
			},
		},
		{
			Input: token2,
			Token: define.Token{
				Original:       token2,
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

	for _, tt := range tests {
		token, err := decoder.Decode(tt.Input)
		switch err {
		case nil:
			assert.Empty(t, tt.ErrPrefix)
		default:
			assert.True(t, strings.Contains(err.Error(), tt.ErrPrefix))
		}

		assert.Equal(t, tt.Token, token)
	}
}

func TestAes256WithMetaDecoder(t *testing.T) {
	decoderConfig := Config{
		Type:       "aes256WithMeta",
		Salt:       "bk",
		DecodedIv:  "bkbkbkbkbkbkbkbk",
		DecodedKey: "81be7fc6-5476-4934-9417-6d4d593728db",
	}

	const (
		token1 = "Ymtia2JrYmtia2JrYmtiaxUtdLzrldhHtlcjc1Cwfo1u99rVk5HGe8EjT761brGtKm3H4Ran78rWl85HwzfRgw=="
		token2 = "Ymtia2JrYmtia2JrYmtia/0ZJ3tXGU6OT2oEqyruVbvWr0kNl7AzgSWPsnVzNBYWRULf8XE/mtQBHLas+jYCrw=="
		token3 = "a1b82bada7904f0d92ec8390ab192cba"
	)

	cache := metacache.New()
	cache.Set(token1, define.Token{
		ProfilesDataId: 10001,
		ProxyDataId:    10002,
		BeatDataId:     10003,
	})
	cache.Set(token3, define.Token{
		AppName:        "foobar",
		BizId:          10,
		TracesDataId:   2001,
		MetricsDataId:  2002,
		LogsDataId:     2003,
		ProfilesDataId: 2004,
	})

	decoder := newAes256WithMetaTokenDecoder(decoderConfig, cache)
	assert.Equal(t, decoderTypeAes256WithMeta, decoder.Type())
	assert.False(t, decoder.Skip())

	tests := []struct {
		Input     string
		Token     define.Token
		ErrPrefix string
	}{
		{
			Input: token1,
			Token: define.Token{
				Original:       token1,
				TracesDataId:   1001,
				MetricsDataId:  1002,
				LogsDataId:     1003,
				BizId:          2,
				ProfilesDataId: 10001,
				ProxyDataId:    10002,
				BeatDataId:     10003,
				AppName:        "oneapm-appname",
			},
		},
		{
			Input: token2,
			Token: define.Token{
				Original:       token2,
				TracesDataId:   1001,
				MetricsDataId:  1002,
				LogsDataId:     1003,
				ProfilesDataId: 1004,
				BizId:          2,
				AppName:        "oneapm-appname",
			},
		},
		{
			Input:     "YmstY29sbGVjdG9y",
			Token:     define.Token{},
			ErrPrefix: "invalid prefix-enc",
		},
		{
			Input: token3,
			Token: define.Token{
				AppName:        "foobar",
				BizId:          10,
				TracesDataId:   2001,
				MetricsDataId:  2002,
				LogsDataId:     2003,
				ProfilesDataId: 2004,
			},
		},
	}

	for _, tt := range tests {
		token, err := decoder.Decode(tt.Input)
		switch err {
		case nil:
			assert.Empty(t, tt.ErrPrefix)
		default:
			assert.True(t, strings.Contains(err.Error(), tt.ErrPrefix))
		}

		assert.Equal(t, tt.Token, token)
	}
}

func TestAes256WithMetaDecoderAndFixedBackup(t *testing.T) {
	newConfig := func(mustEmptyToken bool) Config {
		c := &Config{
			// aes256
			Type:       "aes256",
			Salt:       "bk",
			Version:    "v2",
			DecodedIv:  "bkbkbkbkbkbkbkbk",
			DecodedKey: "81be7fc6-5476-4934-9417-6d4d593728db",

			// fixed
			MustEmptyToken: mustEmptyToken,
			TracesDataId:   3001,
			MetricsDataId:  3002,
			LogsDataId:     3003,
			ProfilesDataId: 3004,
		}
		c.Clean()
		return *c
	}

	tests := []struct {
		Input     string
		Token     define.Token
		ErrPrefix string
		Decoder   combinedTokenDecoder
	}{
		{
			Input: "",
			Token: define.Token{
				TracesDataId:   3001,
				MetricsDataId:  3002,
				LogsDataId:     3003,
				ProfilesDataId: 3004,
			},
			Decoder: newCombinedTokenDecoder(newConfig(true)),
		},
		{
			Input:     "foobar",
			Token:     define.Token{},
			Decoder:   newCombinedTokenDecoder(newConfig(true)),
			ErrPrefix: "invalid token",
		},
		{
			Input: "foobar",
			Token: define.Token{
				TracesDataId:   3001,
				MetricsDataId:  3002,
				LogsDataId:     3003,
				ProfilesDataId: 3004,
			},
			Decoder: newCombinedTokenDecoder(newConfig(false)),
		},
	}

	for _, tt := range tests {
		token, err := tt.Decoder.Decode(tt.Input)
		switch err {
		case nil:
			assert.Empty(t, tt.ErrPrefix)
		default:
			assert.True(t, strings.Contains(err.Error(), tt.ErrPrefix))
		}

		assert.Len(t, tt.Decoder.decoders, 2)
		assert.Equal(t, tt.Token, token)
		assert.Equal(t, "aes256", tt.Decoder.Type())
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

		token, err := decoder.Decode(tokenparser.WrapProxyToken(define.Token{
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

		token, err := decoder.Decode(tokenparser.WrapProxyToken(define.Token{}))
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

		token, err := decoder.Decode(tokenparser.WrapProxyToken(define.Token{
			Original:    "invalid",
			ProxyDataId: 999,
		}))
		assert.Equal(t, "reject invalid token: 999/invalid", err.Error())
		assert.Equal(t, int32(0), token.ProxyDataId)
		assert.Equal(t, "", token.Original)
	})
}

func TestBeatDecoder(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		decoder := NewTokenDecoder(Config{
			Type: decoderTypeBeat,
		})
		assert.Equal(t, decoderTypeBeat, decoder.Type())
		assert.False(t, decoder.Skip())

		token, err := decoder.Decode("1001")
		assert.NoError(t, err)
		assert.Equal(t, int32(1001), token.BeatDataId)
		assert.Equal(t, "1001", token.Original)
	})

	t.Run("Empty", func(t *testing.T) {
		decoder := NewTokenDecoder(Config{
			Type: decoderTypeBeat,
		})

		token, err := decoder.Decode("")
		assert.Equal(t, "reject empty dataid", err.Error())
		assert.Equal(t, int32(0), token.BeatDataId)
		assert.Equal(t, "", token.Original)
	})

	t.Run("Invalid", func(t *testing.T) {
		decoder := NewTokenDecoder(Config{
			Type: decoderTypeBeat,
		})

		token, err := decoder.Decode("-1001")
		assert.Equal(t, "reject invalid dataid: -1001", err.Error())
		assert.Equal(t, int32(0), token.BeatDataId)
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
