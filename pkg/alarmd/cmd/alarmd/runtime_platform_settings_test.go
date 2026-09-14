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
	"encoding/json"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/platformsettings"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// A deployment that renders no distribution gets the copy it had before the
// distribution existed: not_configured, answering from its own layer over
// the code defaults, with the compiler facts and the host filter built from
// that answer. Nothing about it is a fault, and the fleet facts say so.
func TestPlatformSettingsWithoutADistributionAreTheDeploymentLayer(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	seven := []string{"备用机", "测试中", "故障中", "运营中[不监控]", "开发中[不监控]", "运营中[无告警]", "开发中[无告警]"}
	enabled := true
	cfg.PhaseTwo.PlatformSettings.HostDisableMonitorStates = &seven
	cfg.PhaseTwo.PlatformSettings.IsAccessBKData = &enabled
	now := time.Unix(1_700_000_000, 0)
	cache, err := buildPlatformSettings(context.Background(), cfg, nil, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if stats := cache.Stats(); stats.Mode != platformsettings.ModeNotConfigured {
		t.Fatalf("mode without a distribution = %s, want not_configured", stats.Mode)
	}
	current := cache.Current()
	if !reflect.DeepEqual(current.HostDisableMonitorStates, seven) || !current.IsAccessBKData ||
		!reflect.DeepEqual(current.FileSystemTypeIgnore, platformsettings.CodeDefaults().FileSystemTypeIgnore) {
		t.Fatalf("current = %+v, want the deployment layer over the code defaults", current)
	}
	facts := legacyQueryRuntimeFacts(cfg, current)
	if facts.AccessBKData == nil || !*facts.AccessBKData || !reflect.DeepEqual(facts.SystemDiskFilter.Values, []string{"iso9660", "tmpfs", "udf"}) {
		t.Fatalf("compiler facts = %+v", facts)
	}
	hostStatus := newDynamicHostStatusFilter(current.HostDisableMonitorStates)
	if got := len(hostStatus.States()); got != 7 {
		t.Fatalf("states in force = %d, want the deployment's 7", got)
	}
	fleetFacts := platformSettingsFactsSource(cache, func() time.Time { return now })()
	if fleetFacts.Mode != "not_configured" || fleetFacts.StaleBeyondBound || fleetFacts.AuthoritativeAgeSeconds != nil {
		t.Fatalf("fleet facts = %+v, want not_configured with no age and no staleness", fleetFacts)
	}
	// The refresher on a copy with no source changes nothing and reports the
	// filter in force.
	recorder := metric.NewRecorder(metric.BuildInfo{})
	platformSettingsRefresher(cache, hostStatus, recorder)(context.Background())
	if got := len(hostStatus.States()); got != 7 {
		t.Fatalf("states in force after a refresh without a source = %d, want 7", got)
	}
}

// fakePlatformSource is the platform's distribution as a test states it.
type fakePlatformSource struct {
	publication platformsettings.Publication
}

func (source *fakePlatformSource) Read(context.Context, []platformsettings.Field) (platformsettings.Publication, error) {
	return source.publication, nil
}

func publishedPlatformSettings(revision string, values map[platformsettings.Field]string) platformsettings.Publication {
	publication := platformsettings.Publication{Published: true, Revision: revision, Values: map[platformsettings.Field]json.RawMessage{}}
	for field, value := range values {
		publication.Values[field] = json.RawMessage(value)
	}
	return publication
}

func diskFilterOf(t *testing.T, catalog controlplane.Catalog) []string {
	t.Helper()
	if len(catalog.QueryGroups) != 1 {
		t.Fatalf("catalog groups = %d, dispositions %+v", len(catalog.QueryGroups), catalog.Dispositions)
	}
	for _, field := range catalog.QueryGroups[0].QueryPlan.QueryList[0].Conditions.Fields {
		if field.Field != "device_type" {
			continue
		}
		values := make([]string, 0, len(field.Values))
		for _, value := range field.Values {
			values = append(values, value.StringValue)
		}
		return values
	}
	t.Fatalf("query carries no device_type condition: %+v", catalog.QueryGroups[0].QueryPlan.QueryList[0].Conditions)
	return nil
}

// A setting the platform publishes reaches the compiled plans through the
// copy: the round after the copy refreshed compiles by the new value, and
// the round before it compiled by the old one. Nothing is rebuilt or
// restarted in between; the compiler reads the copy when each round opens.
func TestPlatformSettingsChangeReachesThePlansOnTheNextRound(t *testing.T) {
	ctx := context.Background()
	cfg := validGoAccessRuntimeConfig()
	source := &fakePlatformSource{publication: publishedPlatformSettings("r1", map[platformsettings.Field]string{
		platformsettings.FieldFileSystemTypeIgnore: `["iso9660","tmpfs","udf"]`,
	})}
	now := time.Unix(1_700_000_000, 0)
	cache, err := platformsettings.New(platformsettings.Options{
		Source: source, Deployment: cfg.PhaseTwo.PlatformSettings.Layer(), Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	cache.Refresh(ctx)
	planner, err := newPlatformBoundPlanner(cfg, cache)
	if err != nil {
		t.Fatal(err)
	}
	strategies := []controlplane.SourceStrategy{{
		SourceID: "301", Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: strconv.Itoa(controlledG4SyntheticBusinessID), SpaceScope: controlledG4SyntheticSpaceUID},
		Document: controlledG4StrategyDocument(t, 301, strategy.DetectorKindThreshold, "in_use", "system.disk", []string{"mount_point"},
			[]any{map[string]any{"method": "gte", "threshold": 90}}),
	}}
	candidates := controlplane.NewCandidateCache()
	before, err := controlplane.BuildCatalog(ctx, controlplane.BuildRequest{Strategies: strategies, Planner: planner, Cache: candidates})
	if err != nil {
		t.Fatal(err)
	}
	if got := diskFilterOf(t, before); !reflect.DeepEqual(got, []string{"iso9660", "tmpfs", "udf"}) {
		t.Fatalf("disk filter before the change = %v", got)
	}
	// The platform publishes another list; the copy has not read it yet, so
	// the next round still compiles by the old one.
	source.publication = publishedPlatformSettings("r2", map[platformsettings.Field]string{
		platformsettings.FieldFileSystemTypeIgnore: `["tmpfs"]`,
	})
	unchanged, err := controlplane.BuildCatalog(ctx, controlplane.BuildRequest{Strategies: strategies, Planner: planner, Cache: candidates})
	if err != nil {
		t.Fatal(err)
	}
	if compiled, reused := candidates.Stats(); compiled != 0 || reused != 1 || unchanged.SnapshotRevision != before.SnapshotRevision {
		t.Fatalf("before the copy refreshed: compiled=%d reused=%d revision moved=%t, want the old plan reused",
			compiled, reused, unchanged.SnapshotRevision != before.SnapshotRevision)
	}
	cache.Refresh(ctx)
	after, err := controlplane.BuildCatalog(ctx, controlplane.BuildRequest{Strategies: strategies, Planner: planner, Cache: candidates})
	if err != nil {
		t.Fatal(err)
	}
	if compiled, reused := candidates.Stats(); compiled != 1 || reused != 0 {
		t.Fatalf("after the copy refreshed: compiled=%d reused=%d, want the strategy compiled again", compiled, reused)
	}
	if got := diskFilterOf(t, after); !reflect.DeepEqual(got, []string{"tmpfs"}) {
		t.Fatalf("disk filter after the change = %v, want the published list", got)
	}
	if after.SnapshotRevision == before.SnapshotRevision {
		t.Fatal("a plan compiled by another setting must move the Catalog revision")
	}
}
