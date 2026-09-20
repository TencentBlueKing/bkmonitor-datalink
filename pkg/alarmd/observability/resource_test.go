// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

import (
	"testing"
	"time"
)

func TestResourceGovernorObservesSoftHardResumeWithoutActions(t *testing.T) {
	t.Parallel()

	governor, err := NewResourceGovernor(ResourceGovernorConfig{
		Thresholds: map[Resource]ResourceThreshold{
			ResourceRSS: {Resume: 50, Soft: 80, Hard: 100},
		},
		SustainSamples: 2,
		ResumeSamples:  2,
	})
	if err != nil {
		t.Fatalf("NewResourceGovernor() error = %v", err)
	}

	observe := func(value float64) ResourceSignal {
		return governor.Observe(ResourceSnapshot{ObservedAt: time.Now(), RSSBytes: value})
	}
	if got := observe(90); got.State != ResourceNormal {
		t.Fatalf("first soft sample = %#v, want normal", got)
	}
	if got := observe(90); got.State != ResourceSoft || len(got.Reasons) != 1 || got.Reasons[0] != ReasonRSS {
		t.Fatalf("second soft sample = %#v", got)
	}
	if got := observe(120); got.State != ResourceSoft {
		t.Fatalf("first hard sample = %#v, want soft", got)
	}
	if got := observe(120); got.State != ResourceHard {
		t.Fatalf("second hard sample = %#v, want hard", got)
	}
	if got := observe(40); got.State != ResourceHard {
		t.Fatalf("first resume sample = %#v, want hard", got)
	}
	if got := observe(40); got.State != ResourceResume {
		t.Fatalf("second resume sample = %#v, want resume", got)
	}
	if got := observe(40); got.State != ResourceNormal {
		t.Fatalf("stable resumed sample = %#v, want normal", got)
	}

	snapshot := governor.ResourceSnapshot()
	if snapshot.RSSBytes != 40 {
		t.Fatalf("latest resource snapshot = %#v", snapshot)
	}
}

func TestResourceGovernorWithoutThresholdsOnlyObserves(t *testing.T) {
	t.Parallel()

	governor, err := NewResourceGovernor(ResourceGovernorConfig{})
	if err != nil {
		t.Fatalf("NewResourceGovernor() error = %v", err)
	}
	got := governor.Observe(ResourceSnapshot{RSSBytes: 1 << 30, ConsumerLagRecords: 1000, StateBytes: 2048})
	if got.State != ResourceNormal || len(got.Reasons) != 0 {
		t.Fatalf("observe-only signal = %#v, want normal", got)
	}
	if snapshot := governor.ResourceSnapshot(); snapshot.ConsumerLagRecords != 1000 || snapshot.StateBytes != 2048 {
		t.Fatalf("resource observation was not retained: %#v", snapshot)
	}
}

func TestResourceGovernorUnknownConfiguredSampleDoesNotResume(t *testing.T) {
	t.Parallel()

	governor, err := NewResourceGovernor(ResourceGovernorConfig{
		Thresholds: map[Resource]ResourceThreshold{
			ResourceRSS: {Resume: 50, Soft: 80, Hard: 100},
		},
	})
	if err != nil {
		t.Fatalf("NewResourceGovernor() error = %v", err)
	}
	if got := governor.Observe(ResourceSnapshot{RSSBytes: 100}); got.State != ResourceHard {
		t.Fatalf("hard sample = %#v, want hard", got)
	}
	if got := governor.Observe(ResourceSnapshot{RSSBytes: -1}); got.State != ResourceHard {
		t.Fatalf("unknown sample = %#v, want hard", got)
	}
}

func TestResourceGovernorSoftStateRetainsTriggerReason(t *testing.T) {
	t.Parallel()

	governor, err := NewResourceGovernor(ResourceGovernorConfig{
		Thresholds: map[Resource]ResourceThreshold{
			ResourceRSS: {Resume: 50, Soft: 80, Hard: 100},
		},
	})
	if err != nil {
		t.Fatalf("NewResourceGovernor() error = %v", err)
	}
	if got := governor.Observe(ResourceSnapshot{RSSBytes: 90}); got.State != ResourceSoft {
		t.Fatalf("soft sample = %#v, want soft", got)
	}
	got := governor.Observe(ResourceSnapshot{RSSBytes: 60})
	if got.State != ResourceSoft || len(got.Reasons) != 1 || got.Reasons[0] != ReasonRSS {
		t.Fatalf("soft hold sample = %#v, want retained RSS reason", got)
	}
}

func TestResourceGovernorRejectsInvalidOrNonGatingThresholds(t *testing.T) {
	t.Parallel()

	for name, config := range map[string]ResourceGovernorConfig{
		"invalid_order": {
			Thresholds: map[Resource]ResourceThreshold{ResourceRSS: {Resume: 90, Soft: 80, Hard: 100}},
		},
		"lag_is_observation_only": {
			Thresholds: map[Resource]ResourceThreshold{ResourceConsumerLag: {Resume: 1, Soft: 2, Hard: 3}},
		},
		"state_bytes_is_observation_only": {
			Thresholds: map[Resource]ResourceThreshold{ResourceStateBytes: {Resume: 1, Soft: 2, Hard: 3}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewResourceGovernor(config); err == nil {
				t.Fatal("NewResourceGovernor() returned nil error")
			}
		})
	}
}
