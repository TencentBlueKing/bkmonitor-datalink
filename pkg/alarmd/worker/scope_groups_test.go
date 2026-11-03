// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package worker

import (
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/targetplan"
)

// A group reading is known - a host outside it decided outside - only when
// the group answered in full from current facts. Served past a failed
// refresh, with members refused, or not read, its hosts still admit their
// records and nothing else is decided.
func TestAScopeGroupIsKnownOnlyWhenItAnsweredInFullFromCurrentFacts(t *testing.T) {
	members := map[string]struct{}{"730001": {}}
	for _, c := range []struct {
		name     string
		selector targetplan.SelectorResult
		known    bool
		hosts    int
	}{
		{"answered", targetplan.SelectorResult{State: "OK", Members: members}, true, 1},
		{"answered empty", targetplan.SelectorResult{State: "OKEmpty"}, true, 0},
		{"served past a failed refresh", targetplan.SelectorResult{State: "OK", Members: members, StaleAge: time.Minute}, false, 1},
		{"members refused", targetplan.SelectorResult{State: "Incomplete", Reason: "members_dropped", Members: members}, false, 1},
		{"not in the cache", targetplan.SelectorResult{State: "Unavailable", Reason: "key_missing"}, false, 0},
	} {
		got := scopeGroupMembership(c.selector)
		if got.Known != c.known || len(got.HostIDs) != c.hosts {
			t.Errorf("%s: known %v with %d hosts, want %v with %d", c.name, got.Known, len(got.HostIDs), c.known, c.hosts)
		}
	}
}
