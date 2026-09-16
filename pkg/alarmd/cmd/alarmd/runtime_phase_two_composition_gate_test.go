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
	"errors"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

// A round whose activation did not succeed still publishes what its Catalog is
// made of.
//
// The gauges say what the leader last built. The leader builds one on every
// round that reads its source; whether the fleet was then activated onto it is
// a separate fact with its own signals. Gating the composition on a healthy
// round meant a process whose activation never succeeded published no
// composition at all -- so the state where the partition matters most, an
// activation refusing every round, is exactly the state where it vanishes.
// Five leader generations reported no catalog_objects, no
// catalog_withheld_objects and no catalog_no_data_plans for that reason, while
// every other family on the same scrape kept reporting.
func TestACompositionIsPublishedEvenWhenTheRoundIsNotHealthy(t *testing.T) {
	composed := controlplane.ComposeCatalog(controlplane.Catalog{
		Dispositions: []controlplane.ObjectDisposition{
			{SourceID: "1", Scope: "PLAN", Disposition: controlplane.DispositionAccepted},
			{SourceID: "2", Scope: "PLAN", Disposition: controlplane.DispositionConfigRejected, Reason: "PLAN_INVALID"},
		},
	})

	for name, test := range map[string]struct {
		status      phaseTwoControlRefreshStatus
		refreshErr  error
		composition controlplane.CatalogComposition
		want        bool
	}{
		"a healthy round": {
			status: phaseTwoControlHealthy, composition: composed, want: true,
		},
		"a round that built a Catalog and could not activate onto it": {
			status: phaseTwoControlDegradedLastGood, composition: composed, want: true,
		},
		"a round that never composed one": {
			status: phaseTwoControlDegradedLastGood, composition: controlplane.CatalogComposition{}, want: false,
		},
		"a round that failed after composing": {
			// Not the degraded ones -- those return no error and do publish,
			// which is the whole point of this change. This is a round that
			// errored outright, where what is on the result cannot be trusted
			// to describe anything.
			status: phaseTwoControlDegradedLastGood, refreshErr: errRefreshForTest, composition: composed, want: false,
		},
	} {
		t.Run(name, func(t *testing.T) {
			got := publishedComposition(test.refreshErr, test.composition, test.status)
			if (got != nil) != test.want {
				t.Fatalf("published = %t, want %t. An absent composition and one full of zeros read the "+
					"same on a scrape, so a round that composed nothing must publish nothing and a round "+
					"that composed something must publish it whatever happened afterwards", got != nil, test.want)
			}
		})
	}
}

var errRefreshForTest = errors.New("the source refresh failed")
