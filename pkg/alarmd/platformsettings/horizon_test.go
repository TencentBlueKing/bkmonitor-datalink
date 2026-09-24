// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package platformsettings

import (
	"context"
	"testing"
)

func seconds(value int64) *int64 { return &value }

// The no-data horizon resolves by presence, highest layer first: a dynamic
// value, else the deployment's values, else one day. The arms are the
// approved contract's acceptance rows, including the one the old fields'
// fall-through rule gets wrong - a dynamic value equal to the default has to
// override a larger deployment value, not fall through to it.
func TestTheNoDataHorizonResolvesByPresenceHighestLayerFirst(t *testing.T) {
	week := int64(7 * 86400)
	for _, arm := range []struct {
		name       string
		dynamic    *Layer
		deployment Layer
		want       int64
		source     HorizonSource
	}{
		{name: "nothing stated: one day", want: 86400, source: HorizonSourceDefault},
		{name: "values state a week", deployment: Layer{NoDataTrackingHorizonSeconds: &week, Origin: HorizonSourceValues},
			want: week, source: HorizonSourceValues},
		{name: "dynamic one day over values a week", deployment: Layer{NoDataTrackingHorizonSeconds: &week, Origin: HorizonSourceValues},
			dynamic: &Layer{NoDataTrackingHorizonSeconds: seconds(86400), Origin: HorizonSourceDynamic},
			want:    86400, source: HorizonSourceDynamic},
		{name: "dynamic states nothing: values stand", deployment: Layer{NoDataTrackingHorizonSeconds: &week, Origin: HorizonSourceValues},
			dynamic: &Layer{Origin: HorizonSourceDynamic}, want: week, source: HorizonSourceValues},
		{name: "dynamic alone", dynamic: &Layer{NoDataTrackingHorizonSeconds: seconds(3600), Origin: HorizonSourceDynamic},
			want: 3600, source: HorizonSourceDynamic},
	} {
		t.Run(arm.name, func(t *testing.T) {
			layers := []Layer{arm.deployment}
			if arm.dynamic != nil {
				layers = []Layer{*arm.dynamic, arm.deployment}
			}
			got := Resolve(CodeDefaults(), layers...)
			if got.NoDataTrackingHorizonSeconds != arm.want || got.NoDataTrackingHorizonSource != arm.source {
				t.Fatalf("horizon = %d from %s, want %d from %s", got.NoDataTrackingHorizonSeconds,
					got.NoDataTrackingHorizonSource, arm.want, arm.source)
			}
		})
	}
}

// Through the copy, as the distribution reaches it: a published horizon
// overrides the deployment's, a published null withdraws the override and the
// deployment's stands again, and a value that is not a positive whole number
// of seconds is refused - the copy keeps the last good settings rather than
// taking a zero, a fraction or a string as an intention.
func TestTheCopyTakesAPublishedHorizonWithdrawsItOnNullAndRefusesNonsense(t *testing.T) {
	week := int64(7 * 86400)
	fixture := newCacheFixture(t, Layer{NoDataTrackingHorizonSeconds: &week, Origin: HorizonSourceValues})
	ctx := context.Background()
	current := func() (int64, HorizonSource) {
		settings := fixture.cache.Current()
		return settings.NoDataTrackingHorizonSeconds, settings.NoDataTrackingHorizonSource
	}
	if got, source := current(); got != week || source != HorizonSourceValues {
		t.Fatalf("before any publication = %d from %s, want the deployment's week", got, source)
	}

	fixture.source.publication = published("r1", map[Field]string{FieldNoDataTrackingHorizonSeconds: "86400"})
	fixture.cache.Refresh(ctx)
	if got, source := current(); got != 86400 || source != HorizonSourceDynamic {
		t.Fatalf("published one day = %d from %s, want it to override the deployment's week", got, source)
	}

	fixture.source.publication = published("r2", map[Field]string{FieldNoDataTrackingHorizonSeconds: "null"})
	fixture.cache.Refresh(ctx)
	if got, source := current(); got != week || source != HorizonSourceValues {
		t.Fatalf("published null = %d from %s, want the deployment's week back", got, source)
	}

	fixture.source.publication = published("r3", map[Field]string{FieldNoDataTrackingHorizonSeconds: "3600"})
	fixture.cache.Refresh(ctx)
	for _, nonsense := range []string{"0", "-60", "1.5", `"3600"`, "true"} {
		fixture.source.publication = published("r4", map[Field]string{FieldNoDataTrackingHorizonSeconds: nonsense})
		fixture.cache.Refresh(ctx)
		if got, source := current(); got != 3600 || source != HorizonSourceDynamic {
			t.Fatalf("after publishing %s = %d from %s, want the last good 3600 kept", nonsense, got, source)
		}
		if fixture.cache.Stats().Unavailable[UnavailableDecodeError] == 0 {
			t.Fatalf("publishing %s was not counted as a refused publication", nonsense)
		}
	}
}

// The field lives in the strategy domain of the platform's dynamic
// configuration, where the publisher is to declare it.
func TestTheHorizonIsReadFromTheStrategyDomain(t *testing.T) {
	if got := FieldNoDataTrackingHorizonSeconds.DBKey(); got != "base_config.domains.strategy.no_data_tracking_horizon_seconds" {
		t.Fatalf("DBKey = %q", got)
	}
}
