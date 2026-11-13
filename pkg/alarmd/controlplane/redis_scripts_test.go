// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package controlplane

import (
	"testing"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/redisscript"
)

// The renewal and the restore of a snapshot publication are one text, so a
// client cannot tell them apart: they read together, under a name describing
// the text. Had each its own, whichever registered last would take all of both.
func TestTheRenewalAndTheRestoreReadTogetherUnderOneName(t *testing.T) {
	const name = "controlplane_refresh_snapshot_publication"
	if renewSnapshotPublicationScript != restoreSnapshotPublicationScript {
		t.Fatal("the two texts now differ, so each can have its own name: give them one")
	}
	for label, text := range map[string]string{"renewal": renewSnapshotPublicationScript, "restore": restoreSnapshotPublicationScript} {
		if script, _, _ := redisscript.Resolve("eval", []interface{}{"eval", text}); script != name {
			t.Errorf("the %s by text reads as %q", label, script)
		}
		if script, _, _ := redisscript.Resolve("evalsha", []interface{}{"evalsha", redis.NewScript(text).Hash()}); script != name {
			t.Errorf("the %s by digest reads as %q", label, script)
		}
	}
}
