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
	"context"
	"fmt"
	"time"

	"github.com/grafana/pyroscope-go"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type ProfileCollector struct {
	ctx    context.Context
	dataId string
	config MetricOptions
}

type MetricOption func(options *MetricOptions)

type MetricOptions struct {
	enabledProfile bool
	profileAddress string
	profileToken   string
	profileAppIdx  string

	reportInterval time.Duration
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

// ProfileToken profile report token
func ProfileToken(t string) MetricOption {
	return func(options *MetricOptions) {
		options.profileToken = t
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
		options.profileAppIdx = defaultV
	}
}

func MetricReportInterval(t time.Duration) MetricOption {
	return func(options *MetricOptions) {
		options.reportInterval = t
	}
}

func NewProfileCollector(ctx context.Context, o MetricOptions, dataId string) ProfileCollector {
	return ProfileCollector{config: o, ctx: ctx, dataId: dataId}
}

func (r *ProfileCollector) StartReport() {
	if r.config.enabledProfile {
		go r.startProfiling(r.dataId, r.config.profileAppIdx)
	}
}

func (r *ProfileCollector) startProfiling(dataId, appIdx string) {
	appName := fmt.Sprintf("apm_precalculate-%s", appIdx)
	profiler, err := pyroscope.Start(pyroscope.Config{
		ApplicationName: appName,
		ServerAddress:   r.config.profileAddress,
		Logger:          apmLogger,
		AuthToken:       r.config.profileToken,
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
		apmLogger.Errorf("Start pyroscope failed, profile data not be reported, error: %s", err)
		return
	}
	apmLogger.Infof("Start profiling at %s(name: %s)", r.config.profileAddress, appName)

	for {
		select {
		case <-r.ctx.Done():
			if err = profiler.Stop(); err != nil {
				apmLogger.Errorf("receive context done, failed to stop profiler, error: %s", err)
				return
			}
			apmLogger.Infof("received context done, stop profiler successfully")
			return
		}
	}
}
