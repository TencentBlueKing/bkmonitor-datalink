// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package ownership

import (
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// The content switch margin is the one clock bound alarmd assumes, five
// seconds, and not a number of its own.
func TestTheContentSwitchMarginIsTheOneClockBound(t *testing.T) {
	if ContentSwitchMargin != execution.MaxClockSkew || execution.MaxClockSkew != 5*time.Second {
		t.Fatalf("content switch margin %s, clock bound %s; want both 5s and the same", ContentSwitchMargin, execution.MaxClockSkew)
	}
}
