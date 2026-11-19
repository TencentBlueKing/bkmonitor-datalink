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
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
)

// KindUnknown is the kind label of a configured processor whose name this
// build does not register.
const KindUnknown = "unknown"

// Which configuration of a processor failed, for the processor_config_failed
// metrics.
const (
	// ConfigMain is the processor's own configuration at one level (main,
	// platform or privileged): that level's processor is not built.
	ConfigMain = "main"
	// ConfigSub is one sub-configuration, an application's or a service's or
	// instance's within it: it is skipped and they fall back to the main one.
	ConfigSub = "sub"
)

const kindHelp = "kind is the kind of the configured processor, its name before the slash, from a closed set: " +
	"the kinds this build registers, and unknown for a name it does not register, which counts as main. "

var configFailedTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: define.MonitoringNamespace,
		Name:      "processor_config_failed_total",
		Help: "Processor configurations that could not be built, counted on every build. " + kindHelp +
			"main: the processor's own configuration at one level; that level's processor is not built, so a pipeline " +
			"that needs it is left out (pipeline_built_failed_total) or keeps the processor as the level below builds it. " +
			"sub: one application's, service's or instance's sub-configuration; it is skipped and they fall back " +
			"to the processor's main configuration. A reload builds a processor more than once, so read a rise as " +
			"configuration that did not take effect; processor_config_failing holds what is failing now.",
	},
	[]string{"kind", "config"},
)

var configFailing = promauto.NewGaugeVec(
	prometheus.GaugeOpts{
		Namespace: define.MonitoringNamespace,
		Name:      "processor_config_failing",
		Help: "Processor configurations that could not be built in the configuration now in effect: set when the " +
			"manager is built or reloaded, and kept when a reload fails before it takes effect. " + kindHelp +
			"main: the levels, main, platform and privileged, whose configuration of the kind did not build; such a level " +
			"falls back to the one below, so data may still be handled, but not with the configuration as changed. " +
			"sub: the sub-configurations the instances in effect skipped when they were last built.",
	},
	[]string{"kind", "config"},
)

// PrebuildConfigFailed creates both cells of every registered kind, and the
// main cell of unknown, in the counter and the state, so the first failure
// after a start moves an existing series. It is called once the processors
// are registered, before the first build.
func PrebuildConfigFailed() {
	for _, name := range Names() {
		for _, config := range []string{ConfigMain, ConfigSub} {
			configFailedTotal.WithLabelValues(name, config)
			configFailing.WithLabelValues(name, config)
		}
	}
	configFailedTotal.WithLabelValues(KindUnknown, ConfigMain)
	configFailing.WithLabelValues(KindUnknown, ConfigMain)
}

// skipped counts the sub-configurations skipped by the build CountSkipped is
// running, if any.
var skipped atomic.Pointer[atomic.Int64]

// CountSkipped runs build and returns how many sub-configurations the
// factories it calls skipped. Builds run one at a time, from the manager.
func CountSkipped(build func()) int {
	var n atomic.Int64
	prev := skipped.Swap(&n)
	defer skipped.Store(prev)
	build()
	return int(n.Load())
}

// IncConfigFailed counts one configuration of the processor of kind that
// could not be built.
func IncConfigFailed(kind, config string) {
	configFailedTotal.WithLabelValues(kind, config).Inc()
	if config == ConfigSub {
		if n := skipped.Load(); n != nil {
			n.Add(1)
		}
	}
}

// SetConfigFailing sets every cell of the state from the build now in effect:
// main by kind, sub by kind.
func SetConfigFailing(main, sub map[string]int) {
	for _, name := range Names() {
		configFailing.WithLabelValues(name, ConfigMain).Set(float64(main[name]))
		configFailing.WithLabelValues(name, ConfigSub).Set(float64(sub[name]))
	}
	configFailing.WithLabelValues(KindUnknown, ConfigMain).Set(float64(main[KindUnknown]))
}
