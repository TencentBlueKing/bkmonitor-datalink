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
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

// catalogCompositionCollector reports what the Catalog the leader last built
// is made of, read at scrape from the process's own state.
//
// It exists because the deployment could say how many Query Groups it owned
// and not which data sources they came from, so "the polling data sources
// are not onboarded" and "nothing at all is being compiled" read the same on
// every series there was -- and the second was the true one, for days. These
// families answer the first question directly, and the disposition partition
// answers the other half of it: of the strategies that did not become Query
// Groups, what became of them instead.
type catalogCompositionCollector struct {
	mu          sync.Mutex
	source      func() *controlplane.CatalogComposition
	queryGroups *prometheus.Desc
	plans       *prometheus.Desc
	objects     *prometheus.Desc
	inertPlans  *prometheus.Desc
}

func newCatalogCompositionCollector() *catalogCompositionCollector {
	descriptor := func(name, help string, labels ...string) *prometheus.Desc {
		return prometheus.NewDesc(prometheus.BuildFQName(metricNamespace, metricSubsystem, name), help, labels, nil)
	}
	const partition = "Every Query Group falls under exactly one source_semantics, so this family adds up to " +
		"the Catalog's total. A Query Group whose query reads more than one data source is counted once, " +
		"under mixed, rather than once per source: counting it per source would make the family stop adding " +
		"up, and spelling the combination out would make the label set every subset of the sources. A source " +
		"outside the supported list is other, which should stay zero while the compiler refuses such a " +
		"source. Each supported source is reported at zero when it has no Query Groups, because a source " +
		"that compiles nothing must not read the same as a source nobody asked about. Reported by the " +
		"leader only: no other replica builds a Catalog. "
	return &catalogCompositionCollector{
		queryGroups: descriptor("catalog_query_groups",
			"Query Groups in the Catalog the leader last built, by the data sources their query reads. "+
				partition+
				"A Query Group is one query shared by every Plan that wants it, so this counts querying, not "+
				"strategies -- catalog_plans counts those.", "source_semantics"),
		plans: descriptor("catalog_plans",
			"Plans in the Catalog the leader last built, by the data sources their Query Group reads. "+
				partition+
				"Against catalog_query_groups this says how many strategies share each query.", "source_semantics"),
		objects: descriptor("catalog_objects",
			"Source objects the Catalog the leader last built recorded a disposition for, by disposition. "+
				"A partition: every object counted once, including one the control plane added without "+
				"listing here, which lands under other so the partition keeps adding up. ACCEPTED became "+
				"Plans; the rest did not, and the object page says which strategies. Reported by the "+
				"leader only.", "disposition"),
		inertPlans: descriptor("catalog_inert_plans",
			"Plans in the Catalog the leader last built whose schedule cannot hold the wait their data "+
				"needs to land: their readiness boundary falls past their own completion deadline, so every "+
				"round binds every consumer unavailable and the Plan detects nothing, forever. They are "+
				"ACCEPTED and scheduled and execute, and nothing else says so -- the per-round "+
				"unavailability is indistinguishable from any other, which is why this exists. It is a "+
				"subset of catalog_plans, not a partition of it, and a steady zero is the expected reading. "+
				"Read it against sum(catalog_plans): a family whose expected value is zero cannot tell "+
				"'no Plan is inert' from 'nothing checked', and that sum is what says the check ran, "+
				"because the same loop over the same Plans produces both. Reported by the leader only."),
	}
}

func (c *catalogCompositionCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.queryGroups
	ch <- c.plans
	ch <- c.objects
	ch <- c.inertPlans
}

func (c *catalogCompositionCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	source := c.source
	c.mu.Unlock()
	if source == nil {
		return
	}
	composition := source()
	if composition == nil {
		return
	}
	for semantics, count := range composition.QueryGroups {
		ch <- prometheus.MustNewConstMetric(c.queryGroups, prometheus.GaugeValue, float64(count), semantics)
	}
	for semantics, count := range composition.Plans {
		ch <- prometheus.MustNewConstMetric(c.plans, prometheus.GaugeValue, float64(count), semantics)
	}
	for disposition, count := range composition.Objects {
		ch <- prometheus.MustNewConstMetric(c.objects, prometheus.GaugeValue, float64(count), string(disposition))
	}
	ch <- prometheus.MustNewConstMetric(c.inertPlans, prometheus.GaugeValue, float64(composition.InertPlans))
}

// SetCatalogCompositionSource binds the process's last built Catalog
// composition to the collector. Until it is bound, and on any replica that
// never builds one, the collector emits nothing.
func (r *Recorder) SetCatalogCompositionSource(source func() *controlplane.CatalogComposition) {
	if r == nil || r.phaseTwo.catalogComposition == nil {
		return
	}
	r.phaseTwo.catalogComposition.mu.Lock()
	r.phaseTwo.catalogComposition.source = source
	r.phaseTwo.catalogComposition.mu.Unlock()
}
