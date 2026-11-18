// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import (
	"context"
	"runtime/debug"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

// 恢复 panic 的位置，封闭集合。
const (
	// PanicSiteHTTPHandler 是 HTTP 请求处理链上恢复的 panic。
	PanicSiteHTTPHandler = "http_handler"
	// PanicSiteQueryWorker 是查询并发池和查询协程里恢复的 panic。
	PanicSiteQueryWorker = "query_worker"
	// PanicSiteStorage 是存储后端客户端里恢复的 panic。
	PanicSiteStorage = "storage"
)

// PanicSites 列出全部位置。
var PanicSites = []string{PanicSiteHTTPHandler, PanicSiteQueryWorker, PanicSiteStorage}

var panicsTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "unify_query",
		Name:      "panics_total",
		Help:      "Panics recovered by unify-query, by where they were recovered; the result in flight is lost or answered as a failure.",
	},
	[]string{"site"},
)

func init() {
	// 三个位置在启动时建出，值为 0。
	for _, site := range PanicSites {
		panicsTotal.WithLabelValues(site)
	}
}

// PanicInc 记一次恢复的 panic。
func PanicInc(ctx context.Context, site string) {
	metric, _ := panicsTotal.GetMetricWithLabelValues(site)
	counterInc(ctx, metric)
}

// PanicHandler 是 ants 池的 panic 处理函数：计数并记录堆栈。设置了处理函数后，ants 不再自己打印堆栈。
func PanicHandler(ctx context.Context, site string) func(any) {
	return func(recovered any) {
		PanicInc(ctx, site)
		log.Errorf(ctx, "recovered panic in %s: %v\n%s", site, recovered, debug.Stack())
	}
}
