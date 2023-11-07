// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package kits

import (
	"time"
)

const (
	WaitPeriod = 5 * time.Second
)

type RateBus struct {
	ch       chan struct{}
	duration time.Duration
	timer    *time.Timer
}

func NewDefaultRateBus() *RateBus {
	return NewRateBus(WaitPeriod)
}

func NewRateBus(duration time.Duration) *RateBus {
	timer := time.NewTimer(duration)
	timer.Stop()

	ch := make(chan struct{}, 1)
	go func() {
		for range timer.C {
			ch <- struct{}{}
		}
	}()
	return &RateBus{
		ch:       ch,
		duration: duration,
		timer:    timer,
	}
}

func (b *RateBus) Subscribe() <-chan struct{} {
	return b.ch
}

func (b *RateBus) Publish() {
	b.timer.Reset(b.duration)
}

type Alarmer struct {
	t *time.Ticker
}

func NewAlarmer(d time.Duration) *Alarmer {
	return &Alarmer{t: time.NewTicker(d)}
}

func (a *Alarmer) Alarm() bool {
	select {
	case <-a.t.C:
		return true
	default:
		return false
	}
}
