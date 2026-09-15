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
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openalerts"
)

type staticOpenAlertSource struct{ publication openalerts.Publication }

func (source staticOpenAlertSource) Read(context.Context, []openalerts.StrategyKey) (openalerts.Publication, error) {
	return source.publication, nil
}

// The replica's published facts about its copy: the age is absent until a
// publication has been read, and present as the seconds since once it has;
// the mode and the stale flag are the copy's own. The port adapter turns
// the Plans the worker names into the strategy keys the copy reads.
func TestOpenAlertSetFactsAndPortAdapter(t *testing.T) {
	at := time.Unix(1_700_000_000, 0)
	now := func() time.Time { return at }
	source := &staticOpenAlertSource{}
	cache, err := openalerts.New(openalerts.Options{Source: source, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	facts := openAlertSetFactsSource(cache, now)()
	if facts == nil || facts.Mode != string(openalerts.ModeNeverLoaded) || facts.StaleBeyondBound || facts.AuthoritativeAgeSeconds != nil {
		t.Fatalf("facts before any read = %+v, want never_loaded, not stale, no age", facts)
	}

	port := openAlertCopyPort{cache: cache}
	port.TrackPlans([]execution.PlanIdentity{{TenantID: "default", BusinessID: "2", StrategyID: "1001"}})
	source.publication = openalerts.Publication{
		Heartbeat: &openalerts.Heartbeat{PublishedAt: at, Cycle: time.Minute, FingerprintVersion: openalerts.FingerprintVersion},
		Sets:      map[openalerts.StrategyKey][]string{{TenantID: "default", StrategyID: "1001"}: {"f1"}},
	}
	cache.Refresh(context.Background())
	if !port.Contains("default", "1001", "f1") {
		t.Fatal("the tracked strategy's set was not read through the port")
	}
	at = at.Add(45 * time.Second)
	facts = openAlertSetFactsSource(cache, now)()
	if facts.Mode != string(openalerts.ModeAuthoritative) || facts.AuthoritativeAgeSeconds == nil || *facts.AuthoritativeAgeSeconds != 45 {
		t.Fatalf("facts after a read = %+v, want authoritative with age 45", facts)
	}
	if stats := cache.Stats(); stats.Tracked != 1 {
		t.Fatalf("tracked = %d, want the one Plan the worker named", stats.Tracked)
	}
}
