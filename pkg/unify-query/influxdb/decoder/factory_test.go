// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package decoder

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// mockDecoder 用于测试的 mock decoder
type mockDecoder struct{}

func (m *mockDecoder) Decode(ctx context.Context, reader io.Reader, resp *Response) (int, error) {
	return 0, nil
}

func TestGetDecoder(t *testing.T) {
	// 注册一个测试 decoder
	testDecoder := &mockDecoder{}
	decoders["test/decoder"] = testDecoder

	t.Run("get existing decoder", func(t *testing.T) {
		decoder, err := GetDecoder("test/decoder")
		assert.NoError(t, err)
		assert.NotNil(t, decoder)
		assert.Equal(t, testDecoder, decoder)
	})

	t.Run("get non-existing decoder", func(t *testing.T) {
		decoder, err := GetDecoder("non/existing")
		assert.Error(t, err)
		assert.Nil(t, decoder)
		assert.Equal(t, ErrDecoderNotFound, err)
	})

	t.Run("get decoder with empty name", func(t *testing.T) {
		// 默认应该使用 "application/json"
		// 如果 json decoder 已注册，应该能获取到
		decoder, err := GetDecoder("")
		// 如果 json decoder 未注册，应该返回错误
		if err != nil {
			assert.Equal(t, ErrDecoderNotFound, err)
		} else {
			assert.NotNil(t, decoder)
		}
	})
}

func TestDecoderInterface(t *testing.T) {
	// 验证 mockDecoder 实现了 Decoder 接口
	var _ Decoder = &mockDecoder{}

	ctx := context.Background()
	reader := strings.NewReader("test data")
	resp := &Response{}

	decoder := &mockDecoder{}
	size, err := decoder.Decode(ctx, reader, resp)
	assert.NoError(t, err)
	assert.Equal(t, 0, size)
}
