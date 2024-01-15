// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pre_calculate

import (
	"fmt"

	"github.com/grafana/pyroscope-go"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type ProfileCollector struct {
	config      MetricOptions
	runInstance *RunInstance
}

type MetricOption func(options *MetricOptions)

type MetricOptions struct {
	enabledProfile bool
	profileAddress string
	profileAppIdx  string
}

// EnabledProfileReport Whether to enable indicator reporting.
func EnabledProfileReport(e bool) MetricOption {
	return func(options *MetricOptions) {
		if !e {
			logger.Infof("profile report is disabled.")
		}
		options.enabledProfile = e
	}
}

// ProfileAddress profile report host
func ProfileAddress(h string) MetricOption {
	return func(options *MetricOptions) {
		options.profileAddress = h
	}
}

// ProfileAppIdx app name of profile
func ProfileAppIdx(h string) MetricOption {
	return func(options *MetricOptions) {
		if h != "" {
			options.profileAppIdx = h
			return
		}
		defaultV := "apm_precalculate"
		logger.Infof("profile appIdx is not specified, %s is used as the default", defaultV)
		options.profileAppIdx = defaultV
	}
}

func NewProfileCollector(o MetricOptions, instance *RunInstance) ProfileCollector {
	return ProfileCollector{config: o, runInstance: instance}
}

func (r *ProfileCollector) StartReport() {
	if r.config.enabledProfile {
		r.startProfiling(r.runInstance.dataId, r.config.profileAppIdx)
	}
}

func (r *ProfileCollector) startProfiling(dataId, appIdx string) {

	n := fmt.Sprintf("apm_precalculate-%s", appIdx)
	_, err := pyroscope.Start(pyroscope.Config{
		ApplicationName: n,
		ServerAddress:   r.config.profileAddress,
		Logger:          apmLogger,
		Tags:            map[string]string{"dataId": dataId},
		ProfileTypes: []pyroscope.ProfileType{
			// these profile types are enabled by default:
			pyroscope.ProfileCPU,
			pyroscope.ProfileAllocObjects,
			pyroscope.ProfileAllocSpace,
			pyroscope.ProfileInuseObjects,
			pyroscope.ProfileInuseSpace,

			// these profile types are optional:
			pyroscope.ProfileGoroutines,
			pyroscope.ProfileMutexCount,
			pyroscope.ProfileMutexDuration,
			pyroscope.ProfileBlockCount,
			pyroscope.ProfileBlockDuration,
		},
	})

	if err != nil {
		apmLogger.Errorf("Start pyroscope failed, err: %s", err)
		return
	}
	apmLogger.Infof("Start profiling at %s(name: %s)", r.config.profileAddress, n)
}
