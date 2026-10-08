// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import (
	"errors"

	"github.com/prometheus/client_golang/prometheus"
)

// DecodedObjectSource reads the catalog object cache's sampled reading of
// what its objects take decoded against the stored bytes it counts them by.
type DecodedObjectSource func() (samples uint64, last, max float64)

type decodedObjectCollector struct {
	source  DecodedObjectSource
	ratio   *prometheus.Desc
	samples *prometheus.Desc
}

// BindDecodedObjects registers the reading. Bound once.
func (r *Recorder) BindDecodedObjects(source DecodedObjectSource) error {
	if r == nil || r.registry == nil {
		return errors.New("metric: initialized recorder is required")
	}
	if source == nil {
		return errors.New("metric: decoded object source is required")
	}
	return r.registry.Register(&decodedObjectCollector{source: source,
		ratio: prometheus.NewDesc(prometheus.BuildFQName(metricNamespace, metricSubsystem, "object_cache_decoded_ratio"),
			"The catalog object cache's objects sized by their structure against the stored bytes it counts them by, "+
				"one in 64 stored, last and max since the cache was configured. A lower bound on the retained ratio the "+
				"cache's unused budget is charged at (3/2, measured on 1 to 80 Plans a Query Group): a max near or past "+
				"it is production objects outgrowing the shapes the charge was measured on.", []string{"stat"}, nil),
		samples: prometheus.NewDesc(prometheus.BuildFQName(metricNamespace, metricSubsystem, "object_cache_decoded_samples_total"),
			"Objects the reading above has sized.", nil, nil),
	})
}

func (c *decodedObjectCollector) Describe(descriptions chan<- *prometheus.Desc) {
	descriptions <- c.ratio
	descriptions <- c.samples
}

func (c *decodedObjectCollector) Collect(metrics chan<- prometheus.Metric) {
	samples, last, max := c.source()
	metrics <- prometheus.MustNewConstMetric(c.ratio, prometheus.GaugeValue, last, "last")
	metrics <- prometheus.MustNewConstMetric(c.ratio, prometheus.GaugeValue, max, "max")
	metrics <- prometheus.MustNewConstMetric(c.samples, prometheus.CounterValue, float64(samples))
}
