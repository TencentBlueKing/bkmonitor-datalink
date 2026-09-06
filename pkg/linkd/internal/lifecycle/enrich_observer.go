// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lifecycle

import (
	"context"
	"time"

	"linkd/internal/domain"
)

// EnrichChainKind 描述 EventSource 实际使用的丰富链类型。
type EnrichChainKind string

const (
	EnrichChainConfigured EnrichChainKind = "configured"
	EnrichChainNoop       EnrichChainKind = "noop"
	EnrichChainUnknown    EnrichChainKind = "unknown"
)

const (
	EnrichOutcomeCompleted     = "completed"
	EnrichOutcomeError         = "error"
	EnrichOutcomePanic         = "panic"
	EnrichOutcomeInvalidStatus = "invalid_status"
	EnrichOutcomeInvalidData   = "invalid_data"
)

// EnrichObservation 是一次同步丰富完成后的低基数观测事实。
type EnrichObservation struct {
	EventSourceID string
	Status        domain.EnrichStatus
	Outcome       string
	ChainKind     EnrichChainKind
	Duration      time.Duration
	PayloadBytes  int64
}

// EnrichObserver 观察新 Alert 的同步丰富调用。
type EnrichObserver interface {
	Started(ctx context.Context, eventSourceID string)
	Finished(ctx context.Context, observation EnrichObservation)
}

// EnrichRouteClassifier 提供 EventSource 对应的丰富链类型。
type EnrichRouteClassifier interface {
	EnrichChainKind(eventSourceID string) EnrichChainKind
}

type noopEnrichObserver struct{}

func (noopEnrichObserver) Started(context.Context, string) {}

func (noopEnrichObserver) Finished(context.Context, EnrichObservation) {}

// ProcessorOption 配置 Processor 的可选能力。
type ProcessorOption func(*Processor)

// WithEnrichObserver 注入同步丰富观察器。
func WithEnrichObserver(observer EnrichObserver) ProcessorOption {
	return func(processor *Processor) {
		if observer != nil {
			processor.enrichObserver = observer
		}
	}
}
