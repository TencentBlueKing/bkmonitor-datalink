// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package progress

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/go-redis/redis/v8"
)

func TestObservationKeysUseProductionNamespaceAndPhysicalKey(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	defer client.Close()
	control, err := ownership.NewRedisStoreWithClient(client, "fixture:ownership")
	if err != nil {
		t.Fatal(err)
	}
	keys, err := NewObservationKeys("fixture:schedule", control)
	if err != nil {
		t.Fatal(err)
	}
	group := execution.QueryGroupIdentity("query-group")
	want, err := control.ObservationControlKey(group, "fixture:schedule:progress")
	if err != nil {
		t.Fatal(err)
	}
	got, err := keys.ObservationKey(group)
	if err != nil || got != want {
		t.Fatalf("key=%q want=%q err=%v", got, want, err)
	}
	if _, err := keys.ObservationKey(""); err == nil {
		t.Fatal("empty query group accepted")
	}
}
