// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cleaner

import (
	"context"
	"errors"
	"fmt"

	"linkd/internal/consume"
	"linkd/internal/domain"
)

// ProcessResult 是 Cleaner 纯计算阶段的确定结果。
type ProcessResult struct {
	Event      domain.Event
	DiscardErr error
}

// Processor 不得执行存储、消息确认或其他外部副作用。
type Processor interface {
	Process(ctx context.Context, message consume.Message) (ProcessResult, error)
}

type MapperProcessor struct{ mapper *Mapper }

func NewMapperProcessor(mapper *Mapper) (*MapperProcessor, error) {
	if mapper == nil {
		return nil, fmt.Errorf("create cleaner processor: mapper must not be nil")
	}
	return &MapperProcessor{mapper: mapper}, nil
}

func (p *MapperProcessor) Process(ctx context.Context, message consume.Message) (ProcessResult, error) {
	event, err := p.mapper.MapMessage(ctx, message)
	if err == nil {
		return ProcessResult{Event: event}, nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return ProcessResult{}, err
	}
	return ProcessResult{DiscardErr: fmt.Errorf("invalid raw event message: %w", err)}, nil
}

var _ Processor = (*MapperProcessor)(nil)
