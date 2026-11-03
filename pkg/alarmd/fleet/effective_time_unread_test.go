// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package fleet

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategycache"
)

// A dependency several replicas could not read is listed once, failing
// since the earliest of them and last read by the latest good read; the
// list is oldest failure first, bounded, and its total never below what one
// replica counted. With none it is absent.
func TestTheDeploymentListsEachUnreadDependencyOnce(t *testing.T) {
	var view View
	aggregateEffectiveTimeUnread(&view, []Snapshot{{Replica: "pod-a"}})
	if view.EffectiveTimeUnread != nil || view.EffectiveTimeUnreadTotal != 0 {
		t.Fatalf("view %+v, want absent", view.EffectiveTimeUnread)
	}
	at := func(seconds int64) *time.Time { value := time.Unix(seconds, 0).UTC(); return &value }
	a := []strategycache.EffectiveTimeUnread{
		{Tenant: "system", Business: "2", Calendar: 7, Class: "decode", FailingSince: *at(500), LastRead: at(300)},
		{Tenant: "system", Business: "3", Class: "read", FailingSince: *at(400)},
	}
	b := []strategycache.EffectiveTimeUnread{
		{Tenant: "system", Business: "2", Calendar: 7, Class: "timeout", FailingSince: *at(450), LastRead: at(200)},
	}
	want := []strategycache.EffectiveTimeUnread{
		{Tenant: "system", Business: "3", Class: "read", FailingSince: *at(400)},
		{Tenant: "system", Business: "2", Calendar: 7, Class: "timeout", FailingSince: *at(450), LastRead: at(300)},
	}
	// Whichever replica is read first.
	pa := Snapshot{Replica: "pod-a", EffectiveTimeUnread: a, EffectiveTimeUnreadTotal: 2}
	pb := Snapshot{Replica: "pod-b", EffectiveTimeUnread: b, EffectiveTimeUnreadTotal: 1}
	for _, snapshots := range [][]Snapshot{{pa, pb}, {pb, pa}} {
		aggregateEffectiveTimeUnread(&view, snapshots)
		if !reflect.DeepEqual(view.EffectiveTimeUnread, want) || view.EffectiveTimeUnreadTotal != 2 {
			t.Fatalf("view %+v (%d), want %+v", view.EffectiveTimeUnread, view.EffectiveTimeUnreadTotal, want)
		}
	}
	var many []strategycache.EffectiveTimeUnread
	for index := 0; index < strategycache.MaxEffectiveTimeUnread+2; index++ {
		many = append(many, strategycache.EffectiveTimeUnread{Tenant: "system", Business: fmt.Sprint(index), Class: "read", FailingSince: *at(int64(100 + index))})
	}
	aggregateEffectiveTimeUnread(&view, []Snapshot{{Replica: "pod-a", EffectiveTimeUnread: many[:strategycache.MaxEffectiveTimeUnread], EffectiveTimeUnreadTotal: 40},
		{Replica: "pod-b", EffectiveTimeUnread: many[2:]}})
	if len(view.EffectiveTimeUnread) != strategycache.MaxEffectiveTimeUnread || view.EffectiveTimeUnreadTotal != 40 || view.EffectiveTimeUnread[0].Business != "0" {
		t.Fatalf("bounded view %d (%d) first %+v", len(view.EffectiveTimeUnread), view.EffectiveTimeUnreadTotal, view.EffectiveTimeUnread[0])
	}
	health, _ := json.Marshal(healthOf(&view, ReplicaPart{}, now))
	if !strings.Contains(string(health), `"effective_time_unread_total":40`) || !strings.Contains(string(health), `"effective_time_unread":[{"tenant":"system","business":"0"`) {
		t.Fatalf("health = %s", health)
	}
}
