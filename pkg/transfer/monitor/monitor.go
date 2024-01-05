// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package monitor

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	DefBuckets      = []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60}
	LargeDefBuckets = []float64{1, 5, 10, 15, 20, 30, 60, 120, 300, 600, 1800, 3600}
)

type CounterMixin struct {
	CounterSuccesses prometheus.Counter
	CounterFails     prometheus.Counter
}

func NewCounterMixin(successes, fails prometheus.Counter) *CounterMixin {
	mixin := &CounterMixin{
		CounterSuccesses: successes,
		CounterFails:     fails,
	}

	return mixin
}

type TimeObserverRecord struct {
	*TimeObserver
	StartAt time.Time
}

func (r *TimeObserverRecord) Finish() time.Duration {
	duration := time.Since(r.StartAt)
	r.Observer.Observe(duration.Seconds())
	return duration
}

type TimeObserver struct {
	Observer prometheus.Observer
}

func (o *TimeObserver) Start() *TimeObserverRecord {
	return &TimeObserverRecord{
		TimeObserver: o,
		StartAt:      time.Now(),
	}
}

func NewTimeObserver(observer prometheus.Observer) *TimeObserver {
	return &TimeObserver{Observer: observer}
}
