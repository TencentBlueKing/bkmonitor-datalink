// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package collector

import (
	"context"
	"crypto/subtle"
	"encoding/json"

	"linkd/internal/pluginruntime"
)

// SourceBinding 是配置层返回的来源快照；Secret 仅用于鉴权，不能传给 Python。
type SourceBinding struct {
	Secret  string
	Enabled bool
	Request pluginruntime.Request
	Service *Service
}

// SourceReader 按外部接入标识读取候选来源；共享接入 ID 可以对应多个租户。
type SourceReader interface {
	Lookup(context.Context, string) ([]SourceBinding, error)
}

// Receipts 持久记录已接受请求并返回稳定身份/时间，不负责执行或入业务队列。
type Receipts interface {
	Accept(context.Context, pluginruntime.Request) (pluginruntime.Request, error)
}

// SourceIntake 根据密钥选择可信来源，再持久记录原始输入。
type SourceIntake struct {
	sources  SourceReader
	receipts Receipts
}

// NewSourceIntake 注入动态来源读取和接收记录存储。
func NewSourceIntake(sources SourceReader, receipts Receipts) *SourceIntake {
	return &SourceIntake{sources: sources, receipts: receipts}
}

func (i *SourceIntake) source(ctx context.Context, id, secret string) (SourceBinding, error) {
	if secret == "" {
		return SourceBinding{}, ErrSourceUnauthorized
	}
	candidates, err := i.sources.Lookup(ctx, id)
	if err != nil {
		return SourceBinding{}, err
	}
	for _, source := range candidates {
		if source.Enabled && source.Secret != "" && subtle.ConstantTimeCompare([]byte(source.Secret), []byte(secret)) == 1 {
			return source, nil
		}
	}
	return SourceBinding{}, ErrSourceUnauthorized
}

// Authorize 在读取业务正文前拒绝错误密钥和停用来源。
func (i *SourceIntake) Authorize(ctx context.Context, id, secret string) error {
	_, err := i.source(ctx, id, secret)
	return err
}

// Prepare 重新读取来源快照，避免正文读取期间停用或轮换密钥后继续使用旧授权。
func (i *SourceIntake) Prepare(ctx context.Context, id, secret string, body json.RawMessage) (pluginruntime.Request, *Service, error) {
	source, err := i.source(ctx, id, secret)
	if err != nil {
		return pluginruntime.Request{}, nil, err
	}
	request := source.Request
	request.Mode = "push"
	request.Data = body
	request, err = i.receipts.Accept(ctx, request)
	return request, source.Service, err
}
