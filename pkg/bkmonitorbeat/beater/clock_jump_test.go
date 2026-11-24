// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package beater

import (
	"testing"
	"time"

	"github.com/elastic/beats/libbeat/common"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs/validator"
	"github.com/elastic/go-ucfg"
)

func TestClockJumpThresholdConfigCompatibility(t *testing.T) {
	registerRegexpValidatorOnce.Do(func() {
		registerRegexpValidatorErr = ucfg.RegisterValidator("regexp", validator.ValidateRegex)
	})
	if registerRegexpValidatorErr != nil {
		t.Fatalf("register regexp validator: %v", registerRegexpValidatorErr)
	}

	tests := []struct {
		name      string
		configure func(map[string]interface{})
		want      time.Duration
	}{
		{
			name:      "legacy config without threshold stays disabled",
			configure: func(map[string]interface{}) {},
			want:      0,
		},
		{
			name: "explicit threshold enables restart",
			configure: func(values map[string]interface{}) {
				values["clock_jump_threshold"] = "10m"
			},
			want: 10 * time.Minute,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values := map[string]interface{}{
				"mode":        "daemon",
				"node_id":     "clock-jump-test",
				"ip":          "127.0.0.1",
				"bk_biz_id":   1,
				"bk_cloud_id": 0,
			}
			tt.configure(values)

			cfg, err := common.NewConfigFrom(values)
			if err != nil {
				t.Fatalf("create config: %v", err)
			}
			conf := configs.NewConfig()
			if err := cfg.Unpack(conf); err != nil {
				t.Fatalf("unpack config: %v", err)
			}
			if got := conf.ClockJumpThreshold; got != tt.want {
				t.Fatalf("clock jump threshold = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestMeasureClockJump(t *testing.T) {
	tests := []struct {
		name           string
		wallDelta      time.Duration
		monotonicDelta time.Duration
		want           time.Duration
	}{
		{
			name:           "normal tick",
			wallDelta:      time.Second,
			monotonicDelta: time.Second,
			want:           0,
		},
		{
			name:           "clock jumped forward",
			wallDelta:      24*time.Hour + time.Second,
			monotonicDelta: time.Second,
			want:           24 * time.Hour,
		},
		{
			name:           "clock jumped backward",
			wallDelta:      -24*time.Hour + time.Second,
			monotonicDelta: time.Second,
			want:           -24 * time.Hour,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := measureClockJump(tt.wallDelta, tt.monotonicDelta); got != tt.want {
				t.Fatalf("measureClockJump() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestExceedClockJumpThreshold(t *testing.T) {
	const threshold = 10 * time.Minute
	tests := []struct {
		name string
		jump time.Duration
		want bool
	}{
		{name: "below positive threshold", jump: 10*time.Minute - time.Second, want: false},
		{name: "at positive threshold", jump: threshold, want: true},
		{name: "above positive threshold", jump: 24 * time.Hour, want: true},
		{name: "below negative threshold", jump: -10*time.Minute + time.Second, want: false},
		{name: "at negative threshold", jump: -threshold, want: true},
		{name: "above negative threshold", jump: -24 * time.Hour, want: true},
		{name: "disabled", jump: 24 * time.Hour, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotThreshold := threshold
			if tt.name == "disabled" {
				gotThreshold = 0
			}
			if got := exceedClockJumpThreshold(tt.jump, gotThreshold); got != tt.want {
				t.Fatalf("exceedClockJumpThreshold(%s, %s) = %v, want %v", tt.jump, gotThreshold, got, tt.want)
			}
		})
	}
}

func TestWallClockDelta(t *testing.T) {
	prev := time.Date(2026, time.October, 26, 10, 0, 0, 0, time.UTC)
	now := prev.Add(24 * time.Hour)
	if got := wallClockDelta(prev, now); got != 24*time.Hour {
		t.Fatalf("wallClockDelta() = %s, want %s", got, 24*time.Hour)
	}
}

func TestMonotonicElapsedTimeDoesNotLookLikeClockJump(t *testing.T) {
	prev := time.Now()
	now := prev.Add(24 * time.Hour)
	jump := measureClockJump(wallClockDelta(prev, now), now.Sub(prev))
	if jump != 0 {
		t.Fatalf("normal elapsed time produced clock jump %s", jump)
	}
}
