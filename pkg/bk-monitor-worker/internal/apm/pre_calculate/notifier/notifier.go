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

	"go.uber.org/zap"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/window"
	monitorLogger "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// Notifier interface for receive message, it is the data sources of window processing classes
type Notifier interface {
	Start(errorReceiveChan chan<- error)
	Spans() <-chan []window.StandardSpan
}

// Options is configuration items for all notifier
type Options struct {
	// Configure for difference queue
	kafkaConfig

	ctx context.Context
	// chanBufferSize The maximum amount of cached data in the queue
	chanBufferSize int
	// qps rate limiter
	qps int
}

type Option func(*Options)

// BufferSize queue chan size
func BufferSize(s int) Option {
	return func(args *Options) {
		args.chanBufferSize = s
	}
}

// Context ctx of notifier
func Context(ctx context.Context) Option {
	return func(options *Options) {
		options.ctx = ctx
	}
}

func Qps(q int) Option {
	return func(options *Options) {
		options.qps = q
	}
}

type notifyForm int

const (
	// KafkaNotifier notifier type: kafka
	KafkaNotifier notifyForm = 1 << iota
)

// NewNotifier create notifier
func NewNotifier(form notifyForm, dataId string, options ...Option) (Notifier, error) {
	switch form {
	case KafkaNotifier:
		return newKafkaNotifier(dataId, options...)
	default:
		return emptyNotifierInstance, nil
	}
}

// An emptyNotifier for use when not specified
var (
	emptyNotifierInstance = newEmptyNotifier()
)

type emptyNotifier struct{}

// Spans return empty chan
func (e emptyNotifier) Spans() <-chan []window.StandardSpan {
	return make(chan []window.StandardSpan, 0)
}

// Start empty
func (e emptyNotifier) Start(_ chan<- error) {}

func newEmptyNotifier() Notifier {
	return emptyNotifier{}
}

var logger = monitorLogger.With(
	zap.String("location", "notifier"),
)
