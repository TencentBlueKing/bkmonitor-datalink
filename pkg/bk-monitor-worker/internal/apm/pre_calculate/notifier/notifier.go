// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package notifier

import (
	"context"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/window"
	monitorLogger "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
	"go.uber.org/zap"
	"sync"
)

type Notifier interface {
	Start(errorReceiveChan chan<- error)
	Spans() <-chan []window.StandardSpan
}

// Options is configuration items for all notifier
type Options struct {
	// Configure for difference queue
	KafkaConfig

	ctx context.Context
	// chanBufferSize The maximum amount of cached data in the queue
	chanBufferSize int
}

type Option func(*Options)

// BufferSize queue chan size
func BufferSize(s int) Option {
	return func(args *Options) {
		args.chanBufferSize = s
	}
}

func Context(ctx context.Context) Option {
	return func(options *Options) {
		options.ctx = ctx
	}
}

type NotifyForm int

const (
	KafkaNotifier NotifyForm = 1 << iota
)

func NewNotifier(form NotifyForm, options ...Option) Notifier {

	switch form {
	case KafkaNotifier:
		return newKafkaNotifier(options...)
	default:
		return newEmptyNotifier()
	}

}

// An emptyNotifier for use when not specified
var (
	once                  sync.Once
	emptyNotifierInstance Notifier
)

type emptyNotifier struct{}

func (e emptyNotifier) Spans() <-chan []window.StandardSpan {
	return make(chan []window.StandardSpan, 0)
}

func (e emptyNotifier) Start(_ chan<- error) {}

func newEmptyNotifier() Notifier {
	once.Do(func() {
		emptyNotifierInstance = emptyNotifier{}
	})

	return emptyNotifierInstance
}

var logger = monitorLogger.With(
	zap.String("location", "notifier"),
)
