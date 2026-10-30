// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT

package window

import (
	"encoding/json"
	"errors"
	"time"
)

const (
	// DefaultMaxDuration caps the duration of one collection.
	DefaultMaxDuration = 5 * time.Minute
	// DefaultDelay is the grace period for late finished Events.
	DefaultDelay = 5 * time.Minute
)

// Policy controls the collection duration cap and post-collection grace period.
type Policy struct {
	MaxDuration time.Duration
	Delay       time.Duration
}

type lifecycleState uint8

const (
	lifecycleOpen lifecycleState = iota
	lifecycleExpired
	lifecycleFinished
)

// Window owns one host's deadline and timeout lifecycle.
type Window struct {
	deadlineAtMS int64
	state        lifecycleState
}

// New calculates a deadline from the start time, event config, and policy.
// A missing or invalid duration_sec is treated as zero; Delay is still applied.
func New(startAtMS int64, config json.RawMessage, policy Policy) Window {
	durationMS := CollectionDuration(config, policy.MaxDuration).Milliseconds()
	return Window{
		deadlineAtMS: startAtMS + durationMS + policy.Delay.Milliseconds(),
		state:        lifecycleOpen,
	}
}

// Start creates a host window and immediately expires it if its deadline has passed.
func Start(startAtMS int64, config json.RawMessage, policy Policy, nowMS int64) Window {
	window, _ := New(startAtMS, config, policy).ExpireAt(nowMS)
	return window
}

// FromDeadline restores a window from its persisted deadline for expiration scans.
func FromDeadline(deadlineAtMS int64) Window {
	return Window{deadlineAtMS: deadlineAtMS, state: lifecycleOpen}
}

// DeadlineAtMS returns the window deadline as Unix milliseconds.
func (w Window) DeadlineAtMS() int64 {
	return w.deadlineAtMS
}

// Deadline returns the persisted deadline, or nil after the window is finished.
func (w Window) Deadline() *int64 {
	if w.state == lifecycleFinished {
		return nil
	}
	deadline := w.deadlineAtMS
	return &deadline
}

// ExpireAt advances an open window to expired when nowMS reaches its deadline.
func (w Window) ExpireAt(nowMS int64) (Window, bool) {
	if w.state != lifecycleOpen || !w.ExpiredAt(nowMS) {
		return w, false
	}
	w.state = lifecycleExpired
	return w, true
}

// Finish closes the window and clears its persisted deadline.
func (w Window) Finish() Window {
	w.state = lifecycleFinished
	w.deadlineAtMS = 0
	return w
}

// IsExpired reports whether the timeout transition has been applied.
func (w Window) IsExpired() bool {
	return w.state == lifecycleExpired
}

// ExpiredAt reports whether the deadline has passed; the boundary is inclusive.
func (w Window) ExpiredAt(nowMS int64) bool {
	if w.state == lifecycleFinished {
		return false
	}
	return w.state == lifecycleExpired || nowMS >= w.deadlineAtMS
}

// CollectionDuration reads collector_config.duration_sec and caps it at
// maxDuration. Missing, malformed, or negative values return zero. JSON
// numbers and numeric strings are accepted.
func CollectionDuration(config json.RawMessage, maxDuration time.Duration) time.Duration {
	var root map[string]json.RawMessage
	if json.Unmarshal(config, &root) != nil {
		return 0
	}
	var collector struct {
		Duration json.RawMessage `json:"duration_sec"`
	}
	if err := json.Unmarshal(root["collector_config"], &collector); err != nil {
		return 0
	}
	duration, err := decodeInt64(collector.Duration)
	if err != nil || duration < 0 {
		return 0
	}
	maxMillis := maxDuration.Milliseconds()
	if duration > maxMillis/1000 {
		return time.Duration(maxMillis) * time.Millisecond
	}
	return time.Duration(duration) * time.Second
}

func decodeInt64(raw json.RawMessage) (int64, error) {
	if len(raw) == 0 {
		return 0, errors.New("missing integer")
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return json.Number(text).Int64()
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err != nil {
		return 0, err
	}
	return number.Int64()
}
