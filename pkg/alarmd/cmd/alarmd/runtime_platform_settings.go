// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/admission"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/platformsettings"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// buildPlatformSettings assembles the process copy of the platform's
// settings. With a distribution connection rendered it reads the platform's
// publication; without one it is not_configured and answers from the
// deployment layer and the code defaults, which is what alarmd did before
// it could read the platform. The first read is synchronous: the compiler
// and the admission filters are built from what the copy answers, and a
// process that starts against a readable publication should start on it
// rather than a cycle behind it.
func buildPlatformSettings(ctx context.Context, cfg config.Config, client redis.UniversalClient, now func() time.Time) (*platformsettings.Cache, error) {
	var source platformsettings.Source
	if client != nil {
		redisSource, err := platformsettings.NewRedisSource(client, cfg.PhaseTwo.PlatformSettings.RedisKeyPrefix)
		if err != nil {
			return nil, err
		}
		source = redisSource
	}
	cache, err := platformsettings.New(platformsettings.Options{
		Source: source, Deployment: cfg.PlatformSettingsLayer(), Now: now,
	})
	if err != nil {
		return nil, err
	}
	cache.Refresh(ctx)
	return cache, nil
}

// legacyQueryRuntimeFacts is what the legacy query compiler compiles by:
// the platform settings as the copy answers them now. The two device
// filters' field names are the platform's constants.
func legacyQueryRuntimeFacts(cfg config.Config, settings platformsettings.Settings) controlplane.LegacyQueryRuntimeFacts {
	accessBKData := settings.IsAccessBKData
	facts := controlplane.LegacyQueryRuntimeFacts{
		AccessBKData:          &accessBKData,
		BKDataCMDBLevelTables: append([]string{}, settings.BKDataCMDBLevelTables...),
		SystemDiskFilter: controlplane.LegacyRuntimeFilterFact{
			FieldName: config.SystemDiskFilterField, Values: append([]string{}, settings.FileSystemTypeIgnore...),
		},
		SystemNetworkFilter: controlplane.LegacyRuntimeFilterFact{
			FieldName: config.SystemNetworkFilterField, Values: config.SystemNetworkFilterValues(),
		},
	}
	return facts
}

// newPlanCompiler is the strategy compiler a process evaluates Plans with. It
// lays the aggregation boundaries of a Plan detected more often than it
// aggregates in the zone newPlatformBoundPlanner lays every query in, so a
// boundary is where the query service starts an aligned query's bucket.
func newPlanCompiler(cfg config.Config) (*strategy.PlanCompiler, error) {
	location, err := time.LoadLocation(cfg.PhaseTwo.Control.Timezone)
	if err != nil {
		return nil, errors.New("alarmd: the control timezone is no time zone")
	}
	return strategy.NewCompiler(strategy.NewDefaultAlgorithmCompilerRegistry(), cfg.CompilerLimits(), strategy.WithBoundaryLocation(location))
}

// newPlatformBoundPlanner is the legacy query compiler over the platform
// settings copy: each control round freezes the facts as the copy answers
// them when the round opens, and compiles every strategy by them.
func newPlatformBoundPlanner(cfg config.Config, settings *platformsettings.Cache) (*controlplane.SettingsBoundLegacyCompiler, error) {
	if settings == nil {
		return nil, errors.New("alarmd: the platform settings copy is required to compile by")
	}
	return controlplane.NewSettingsBoundLegacyCompiler(
		execution.ProviderRouteRef(cfg.PhaseTwo.Control.ProviderRoute),
		cfg.PhaseTwo.Control.Timezone,
		func() controlplane.LegacyQueryRuntimeFacts { return legacyQueryRuntimeFacts(cfg, settings.Current()) },
	)
}

// dynamicHostStatusFilter is the host status filter with the states the
// platform settings copy currently answers. The filter itself is immutable;
// this holds the one in force and swaps it when the states change, so the
// access path reads a pointer per plan and never a lock. A filter is always
// in force: built with one, and an empty state list installs one that
// disables no host by state.
type dynamicHostStatusFilter struct {
	current atomic.Pointer[admission.HostStatusFilter]
}

func newDynamicHostStatusFilter(states []string) *dynamicHostStatusFilter {
	filter := &dynamicHostStatusFilter{}
	filter.Apply(states)
	return filter
}

// Apply installs the filter for states and reports how many states are now in
// force. An empty list installs a filter all the same: it disables no host by
// state, and still drops the invalid and unknown hosts Python drops.
func (filter *dynamicHostStatusFilter) Apply(states []string) int {
	installed := admission.NewHostStatusFilter(states)
	filter.current.Store(installed)
	return len(installed.States())
}

// States is what the filter in force decides on, for the surface that
// reports it.
func (filter *dynamicHostStatusFilter) States() []string {
	return filter.current.Load().States()
}

func (*dynamicHostStatusFilter) Name() string { return "host_status" }

func (filter *dynamicHostStatusFilter) Admit(plan admission.PlanContext, facts *admission.Facts) admission.Decision {
	return filter.current.Load().Admit(plan, facts)
}

// platformSettingsRefresher is what the runtime runs once a minute: read the
// distribution, then bring the host status filter and its published count
// up to what the copy now answers.
func platformSettingsRefresher(cache *platformsettings.Cache, hostStatus *dynamicHostStatusFilter, recorder *metric.Recorder) func(context.Context) {
	return func(ctx context.Context) {
		cache.Refresh(ctx)
		states := hostStatus.Apply(cache.Current().HostDisableMonitorStates)
		if recorder != nil {
			recorder.SetHostDisableMonitorStates(states)
		}
	}
}

// platformSettingsFactsSource is what this replica publishes about its copy.
// The age is absent until there has been a publication: a zero would read
// as "just now" on a copy that never loaded.
func platformSettingsFactsSource(cache *platformsettings.Cache, now func() time.Time) func() *fleet.PlatformSettingsFacts {
	return func() *fleet.PlatformSettingsFacts {
		stats := cache.Stats()
		current := cache.Current()
		facts := &fleet.PlatformSettingsFacts{
			Mode: string(stats.Mode), StaleBeyondBound: cache.StaleBeyondBound(), LastUnavailable: stats.LastUnavailable,
			NoDataTrackingHorizonSeconds: current.NoDataTrackingHorizonSeconds,
			NoDataTrackingHorizonSource:  string(current.NoDataTrackingHorizonSource),
		}
		if !stats.LoadedAt.IsZero() {
			age := now().Sub(stats.LoadedAt).Seconds()
			facts.AuthoritativeAgeSeconds = &age
		}
		entries := map[platformsettings.Field]int{
			platformsettings.FieldHostDisableMonitorStates: len(stats.Settings.HostDisableMonitorStates),
			platformsettings.FieldBKDataCMDBLevelTables:    len(stats.Settings.BKDataCMDBLevelTables),
			platformsettings.FieldFileSystemTypeIgnore:     len(stats.Settings.FileSystemTypeIgnore),
		}
		for _, field := range platformsettings.Fields {
			row := fleet.PlatformSettingField{Field: string(field), Source: string(stats.Sources[field])}
			if field == platformsettings.FieldIsAccessBKData {
				enabled := stats.Settings.IsAccessBKData
				row.Enabled = &enabled
			}
			if n, isList := entries[field]; isList {
				row.Entries = &n
			}
			facts.Fields = append(facts.Fields, row)
		}
		return facts
	}
}
