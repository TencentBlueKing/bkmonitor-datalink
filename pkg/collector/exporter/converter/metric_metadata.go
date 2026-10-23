// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package converter

import (
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/metricmeta"
)

var metadataDropped = promauto.NewCounterVec(prometheus.CounterOpts{
	Namespace: define.MonitoringNamespace,
	Name:      "metric_metadata_source_dropped_total", Help: "Metric metadata source drops",
}, []string{"reason"})

func metadataScope(dimensions map[string]string) metricmeta.Scope {
	service, scope := dimensions["service_name"], dimensions["scope_name"]
	if service == "" {
		return metricmeta.Scope{Type: metricmeta.ScopeDefault, Name: scope}
	}
	escape := func(s string) string {
		return strings.ReplaceAll(strings.ReplaceAll(s, "%", "%25"), "-", "%2D")
	}
	return metricmeta.Scope{Type: metricmeta.ScopeAPM, Name: escape(service) + "-" + escape(scope)}
}

func observeMetadata(observer metricmeta.Observer, dataID int32, dims map[string]string, metric string, value metricmeta.Value) {
	if observer != nil {
		observer.Observe(metricmeta.NewKey(dataID, metadataScope(dims), metric), value)
	}
}

func metadataValue(name, originalType, typ, temporality, usage, unit, description string) metricmeta.Value {
	v := metricmeta.Value{
		Name: name, Type: typ, OriginalType: originalType, Temporality: temporality,
		Usage: usage, Unit: unit, Description: description,
	}
	if unit != "" {
		v.Present |= metricmeta.PresentUnit
	}
	if description != "" {
		v.Present |= metricmeta.PresentDescription
	}
	if usage != "" {
		v.Present |= metricmeta.PresentUsage
	}
	return v
}
