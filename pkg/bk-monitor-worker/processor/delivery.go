// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package processor

import (
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/broker"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type Forwarder struct {
	broker broker.Broker

	done   chan struct{}
	queues []string
	// timer interval
	interval time.Duration
}

type ForwarderParams struct {
	Broker   broker.Broker
	Queues   []string
	Interval time.Duration
}

// NewForwarder new a forwarder client
func NewForwarder(params ForwarderParams) *Forwarder {
	return &Forwarder{
		broker:   params.Broker,
		done:     make(chan struct{}),
		queues:   params.Queues,
		interval: params.Interval,
	}
}

func (f *Forwarder) Shutdown() {
	logger.Info("shutting down ...")
	f.done <- struct{}{}
}

// Start goroutine.
func (f *Forwarder) Start(wg *sync.WaitGroup) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		timer := time.NewTimer(f.interval)
		for {
			select {
			case <-f.done:
				logger.Debug("Forwarder done")
				return
			case <-timer.C:
				f.Exec()
				timer.Reset(f.interval)
			}
		}
	}()
}

// Exec check ready
func (f *Forwarder) Exec() {
	if err := f.broker.ForwardIfReady(f.queues...); err != nil {
		logger.Errorf("Failed to forward scheduled tasks: %v", err)
	}
}
