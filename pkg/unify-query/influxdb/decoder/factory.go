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
)

// Decoder 定义了解码器接口
// 用于将不同格式的响应数据解码为统一的 Response 结构
type Decoder interface {
	// Decode 从 reader 中读取数据并解码到 resp 中
	// 参数:
	//   - ctx: 上下文对象
	//   - reader: 数据读取器
	//   - resp: 目标响应对象，解码后的数据将填充到此对象中
	// 返回:
	//   - int: 读取的字节数
	//   - error: 解码过程中的错误，如果成功则为 nil
	Decode(ctx context.Context, reader io.Reader, resp *Response) (int, error)
}

// decoders 存储所有已注册的解码器
// key 为解码器的名称（通常是 MIME 类型，如 "application/json"）
// value 为对应的解码器实现
var decoders = make(map[string]Decoder)

// GetDecoder 根据名称获取对应的解码器
// 参数:
//   - name: 解码器名称（通常是 MIME 类型），如果为空则默认使用 "application/json"
//
// 返回:
//   - Decoder: 找到的解码器实例
//   - error: 如果解码器不存在则返回 ErrDecoderNotFound
func GetDecoder(name string) (Decoder, error) {
	if name == "" {
		name = "application/json"
	}
	if decoder, ok := decoders[name]; ok {
		return decoder, nil
	}
	return nil, ErrDecoderNotFound
}
