// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package timesync

import (
	"errors"
	"testing"
	"time"

	"github.com/beevik/ntp"
)

func withQueryNTP(t *testing.T, fn func(string, ntp.QueryOptions) (*ntp.Response, error)) {
	t.Helper()
	original := queryNTP
	queryNTP = fn
	t.Cleanup(func() {
		queryNTP = original
	})
}

func TestNTPResponseOffsetSeconds(t *testing.T) {
	rsp := &ntp.Response{ClockOffset: -10 * time.Second}
	if got := ntpResponseOffsetSeconds(rsp); got != 10 {
		t.Fatalf("offset = %v, want 10", got)
	}
}

func TestQueryBluekingSuccess(t *testing.T) {
	withQueryNTP(t, func(address string, options ntp.QueryOptions) (*ntp.Response, error) {
		if address != "10.0.0.20:123" {
			t.Fatalf("address = %q, want 10.0.0.20:123", address)
		}
		return &ntp.Response{ClockOffset: -10 * time.Second, Stratum: 1}, nil
	})

	cli := NewClient(&Option{BkNTPSource: "10.0.0.20:123", Timeout: time.Second})
	result, err := cli.QueryBlueking()
	if err != nil {
		t.Fatalf("QueryBlueking error: %v", err)
	}
	if result.Address != "10.0.0.20:123" || result.Target != "ntpd" {
		t.Fatalf("result = %+v", result)
	}
	if result.OffsetSeconds != 10 {
		t.Fatalf("offset = %v, want 10", result.OffsetSeconds)
	}
	if !result.Available {
		t.Fatalf("available = false, want true")
	}
	if delta := time.Until(result.TBlue); delta > -9*time.Second || delta < -11*time.Second {
		t.Fatalf("T_blue delta = %v, want about -10s", delta)
	}
}

func TestQueryBluekingInvalidResponse(t *testing.T) {
	withQueryNTP(t, func(string, ntp.QueryOptions) (*ntp.Response, error) {
		return &ntp.Response{ClockOffset: time.Second, Stratum: 0}, nil
	})

	cli := NewClient(&Option{BkNTPSource: "10.0.0.20:123"})
	if _, err := cli.QueryBlueking(); err == nil {
		t.Fatalf("expected validation error, got nil")
	}
}

func TestQueryBluekingQueryError(t *testing.T) {
	withQueryNTP(t, func(string, ntp.QueryOptions) (*ntp.Response, error) {
		return nil, errors.New("query failed")
	})

	cli := NewClient(&Option{BkNTPSource: "10.0.0.20:123"})
	if _, err := cli.QueryBlueking(); err == nil {
		t.Fatalf("expected query error, got nil")
	}
}

func TestQueryBluekingNotConfigured(t *testing.T) {
	cli := NewClient(&Option{})
	if _, err := cli.QueryBlueking(); err == nil {
		t.Fatalf("expected not configured error, got nil")
	}
}
