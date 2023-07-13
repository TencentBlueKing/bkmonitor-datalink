// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package sizeobserver

import (
	"strconv"
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
)

var sizeObserverMaxBytes = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Namespace: define.MonitoringNamespace,
		Name:      "size_observer_max_bytes",
		Help:      "Size observer max bytes",
	},
	[]string{"id"},
)

func init() {
	prometheus.MustRegister(sizeObserverMaxBytes)
}

type SizeObserver struct {
	mut   sync.RWMutex
	sizes map[int32]int
}

func New() *SizeObserver {
	return &SizeObserver{
		sizes: map[int32]int{},
	}
}

func (o *SizeObserver) ObserveSize(id int32, size int) {
	o.mut.RLock()
	v := o.sizes[id]
	o.mut.RUnlock()

	if v > size {
		return
	}

	o.mut.Lock()
	defer o.mut.Unlock()

	v = o.sizes[id]
	if v < size {
		sizeObserverMaxBytes.WithLabelValues(strconv.Itoa(int(id))).Set(float64(size))
		o.sizes[id] = size
	}
}

func (o *SizeObserver) Get(id int32) int {
	o.mut.RLock()
	defer o.mut.RUnlock()

	return o.sizes[id]
}
