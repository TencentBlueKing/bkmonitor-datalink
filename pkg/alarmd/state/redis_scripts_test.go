// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package state

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/redisscript"
)

// These digests are sent with EvalSha by hand, so that they are the digests of
// the texts is what makes them read as the scripts.
func TestTheDigestsSentByHandReadAsTheirScripts(t *testing.T) {
	for _, test := range []struct{ name, digest string }{
		{"state_fenced_batch_write", fencedBatchWriteSHA},
		{"state_apply_hash_delta", applyHashDeltaSHA},
		{"state_record_hash_field", recordHashFieldSHA},
		{"state_stage_hash_segment", stageHashSegmentSHA},
		{"state_swap_staged_hash", swapStagedHashSHA},
		{"state_discard_staged_hash", discardStagedHashSHA},
		{"state_renew_if_below", renewIfBelowSHA},
	} {
		if script, _, _ := redisscript.Resolve("evalsha", []interface{}{"evalsha", test.digest}); script != test.name {
			t.Errorf("the digest of %s reads as %q", test.name, script)
		}
	}
}
