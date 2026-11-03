// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package fleet

import (
	"sort"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategycache"
)

// aggregateEffectiveTimeUnread merges the replicas' unread effective-time
// dependencies. Replicas share them - one business is read by every replica
// owning one of its Plans - so a dependency is listed once, failing since
// the earliest replica saw it fail and last read by the latest good read.
// The total is a lower bound once a replica's own list was cut: the larger
// of the distinct dependencies seen and any one replica's total.
func aggregateEffectiveTimeUnread(view *View, snapshots []Snapshot) {
	type key struct {
		tenant, business string
		calendar         int64
	}
	merged := map[key]strategycache.EffectiveTimeUnread{}
	total := 0
	for _, snapshot := range snapshots {
		total = max(total, snapshot.EffectiveTimeUnreadTotal)
		for _, unread := range snapshot.EffectiveTimeUnread {
			k := key{unread.Tenant, unread.Business, unread.Calendar}
			prior, seen := merged[k]
			if seen {
				if prior.FailingSince.Before(unread.FailingSince) {
					unread.FailingSince, unread.Class = prior.FailingSince, prior.Class
				}
				if prior.LastRead != nil && (unread.LastRead == nil || prior.LastRead.After(*unread.LastRead)) {
					unread.LastRead = prior.LastRead
				}
			}
			merged[k] = unread
		}
	}
	if len(merged) == 0 {
		view.EffectiveTimeUnread, view.EffectiveTimeUnreadTotal = nil, 0
		return
	}
	out := make([]strategycache.EffectiveTimeUnread, 0, len(merged))
	for _, unread := range merged {
		out = append(out, unread)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].FailingSince.Equal(out[j].FailingSince) {
			return out[i].FailingSince.Before(out[j].FailingSince)
		}
		if out[i].Tenant != out[j].Tenant {
			return out[i].Tenant < out[j].Tenant
		}
		if out[i].Business != out[j].Business {
			return out[i].Business < out[j].Business
		}
		return out[i].Calendar < out[j].Calendar
	})
	total = max(total, len(out))
	if len(out) > strategycache.MaxEffectiveTimeUnread {
		out = out[:strategycache.MaxEffectiveTimeUnread]
	}
	view.EffectiveTimeUnread, view.EffectiveTimeUnreadTotal = out, total
}
