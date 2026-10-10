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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/storecensus"
)

// StoreCensusSource reads the latest census of each store at scrape time:
// the Control Leader's, and nothing on any other replica.
type StoreCensusSource func() []storecensus.Result

// storeCensusCollector reports what the stores hold by key family, as the
// store weighed it (package storecensus). Families are at most
// storecensus.MaxFamilies per store, so a store has at most that many series
// of each family reading.
type storeCensusCollector struct {
	source                                StoreCensusSource
	keys, exact, at                       *prometheus.Desc
	familyKeys, familyBytes, familySample *prometheus.Desc
}

func newStoreCensusCollector(source StoreCensusSource) *storeCensusCollector {
	name := func(suffix string) string {
		return prometheus.BuildFQName(metricNamespace, metricSubsystem, "store_census_"+suffix)
	}
	return &storeCensusCollector{
		source: source,
		keys: prometheus.NewDesc(name("keys"),
			"The store's key count (DBSIZE) at its latest census, taken by the Control Leader every ten minutes; "+
				"no series on any other replica.", []string{"client"}, nil),
		exact: prometheus.NewDesc(name("exact"),
			"1 when the latest census weighed every key of the store, 0 when it sampled them.", []string{"client"}, nil),
		at: prometheus.NewDesc(name("timestamp_seconds"),
			"When the latest census of the store was taken.", []string{"client"}, nil),
		familyKeys: prometheus.NewDesc(name("family_keys"),
			"Keys of one family the store holds, estimated: the family's share of the keys weighed times the "+
				"store's key count. A family is a key's segments with those naming one instance written *.",
			[]string{"client", "family"}, nil),
		familyBytes: prometheus.NewDesc(name("family_bytes"),
			"Bytes the store holds for one family, as MEMORY USAGE weighs its keys, estimated as family_keys is. "+
				"Read it with family_samples, n: its relative standard error is at least 1/sqrt(n), and "+
				"sqrt((1-p+c^2)/n) with p the family's share of keys and c the spread of its key sizes.",
			[]string{"client", "family"}, nil),
		familySample: prometheus.NewDesc(name("family_samples"),
			"Keys of one family the latest census weighed: the family's estimates rest on these.",
			[]string{"client", "family"}, nil),
	}
}

func (c *storeCensusCollector) Describe(descriptions chan<- *prometheus.Desc) {
	for _, description := range []*prometheus.Desc{c.keys, c.exact, c.at, c.familyKeys, c.familyBytes, c.familySample} {
		descriptions <- description
	}
}

func (c *storeCensusCollector) Collect(metrics chan<- prometheus.Metric) {
	for _, result := range c.source() {
		exact := 0.0
		if result.Exact {
			exact = 1
		}
		metrics <- prometheus.MustNewConstMetric(c.keys, prometheus.GaugeValue, float64(result.Keys), result.Store)
		metrics <- prometheus.MustNewConstMetric(c.exact, prometheus.GaugeValue, exact, result.Store)
		metrics <- prometheus.MustNewConstMetric(c.at, prometheus.GaugeValue, float64(result.At.Unix()), result.Store)
		for _, family := range result.Families {
			metrics <- prometheus.MustNewConstMetric(c.familyKeys, prometheus.GaugeValue, family.Keys, result.Store, family.Name)
			metrics <- prometheus.MustNewConstMetric(c.familyBytes, prometheus.GaugeValue, family.Bytes, result.Store, family.Name)
			metrics <- prometheus.MustNewConstMetric(c.familySample, prometheus.GaugeValue, float64(family.Samples), result.Store, family.Name)
		}
	}
}

// BindStoreCensus registers the stores' census. Bound once.
func (r *Recorder) BindStoreCensus(source StoreCensusSource) error {
	if r == nil || r.registry == nil {
		return errors.New("metric: initialized recorder is required")
	}
	if source == nil {
		return errors.New("metric: store census source is required")
	}
	return r.registry.Register(newStoreCensusCollector(source))
}
