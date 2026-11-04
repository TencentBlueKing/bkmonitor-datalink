// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"errors"
	"fmt"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

// A plan carries read is counted by the same split the object detail names:
// a timeline without an open Segment, wrapped or not, is no_open_segment, and
// any other error is failed, never read.
func TestAPlanCarriesReadIsCountedByWhyItFailed(t *testing.T) {
	for _, test := range []struct {
		err  error
		want string
	}{
		{nil, "read"},
		{controlplane.ErrScheduleUnavailable, "no_open_segment"},
		{fmt.Errorf("load timeline: %w", controlplane.ErrScheduleUnavailable), "no_open_segment"},
		{errors.New("redis: connection refused"), "failed"},
	} {
		if got := planCarriesReadResult(test.err); got != test.want {
			t.Errorf("planCarriesReadResult(%v) = %q, want %q", test.err, got, test.want)
		}
	}
}
