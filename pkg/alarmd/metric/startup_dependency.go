// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import "github.com/prometheus/client_golang/prometheus"

// StartupDependencies are the dependencies startup waits on in place when
// they do not answer, rather than exiting. One label value each, so the page
// can say which one a replica that never became ready is waiting for.
var StartupDependencies = []string{
	"redis_source", "redis_runtime", "redis_cmdb", "redis_dynamic_config", "redis_target_group",
	"redis_legacy_output", "ownership_store", "state_store", "cmdb_index",
}

func newStartupDependencyWaits() *prometheus.CounterVec {
	waits := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "startup_dependency_wait_total",
		Help: "Startup attempts on a dependency that did not answer and were retried in place, by dependency. " +
			"The replica stays alive and not ready while this rises; a dependency that answers again ends it " +
			"without a restart. Rising without stopping names the dependency a replica that never joined is " +
			"waiting for. A dependency that refuses outright (a wrong password, a server that cannot run the " +
			"fence) is not counted here: it ends startup.",
	}, []string{"dependency"})
	for _, dependency := range StartupDependencies {
		waits.WithLabelValues(dependency)
	}
	return waits
}

// RecordStartupDependencyWait counts one failed startup attempt that will be
// retried.
func (r *Recorder) RecordStartupDependencyWait(dependency string) {
	if r == nil || r.phaseTwo.startupDependencyWaits == nil || !knownLabel(StartupDependencies, dependency) {
		return
	}
	r.phaseTwo.startupDependencyWaits.WithLabelValues(dependency).Inc()
}
