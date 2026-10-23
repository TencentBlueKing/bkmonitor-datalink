// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package execution

import "errors"

// ShareRefusal is a Slot refused because its own retained bytes would exceed
// the share one Query Group may hold of the replica's pool: the phase the
// reservation was asked in, what the Slot already held, what it asked for, and
// the share it was measured against.
type ShareRefusal struct {
	Phase          string
	OwnUsedBytes   uint64
	RequestedBytes uint64
	LimitBytes     uint64
}

// Same reports whether two refusals are the same decision: the same phase
// against the same share. The byte counts are left out on purpose - a window
// re-read a minute later can carry a few more late points, and that does not
// make the Slot any more likely to fit.
func (refusal ShareRefusal) Same(other ShareRefusal) bool {
	return refusal.Phase == other.Phase && refusal.LimitBytes == other.LimitBytes
}

// DeterministicShareRefusal is implemented by an Execute error that is a share
// refusal. It answers true only when the refusal is deterministic: the share
// flag set and no shared usage behind it, so the Slot's own total alone is
// over the share and no amount of capacity freed elsewhere makes it fit. A
// refusal on the pool (shared usage above zero) answers false: concurrent
// Slots free their part of the pool and a retry can succeed.
type DeterministicShareRefusal interface {
	DeterministicShareRefusal() (ShareRefusal, bool)
}

// ShareRefusalOf is the deterministic share refusal err carries, if any.
func ShareRefusalOf(err error) (ShareRefusal, bool) {
	var refused DeterministicShareRefusal
	if err == nil || !errors.As(err, &refused) {
		return ShareRefusal{}, false
	}
	return refused.DeterministicShareRefusal()
}
