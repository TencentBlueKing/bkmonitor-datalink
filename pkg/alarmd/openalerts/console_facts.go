// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package openalerts

import (
	"context"
	"errors"
	"time"
)

// ConsoleFacts are the two facts the link's Console gives that decide
// whether the open alert sets can answer for this process: where the sets
// are, and how the link keys the alerts in them. Neither is inferred here
// from what the sets happen to hold; a set that lost one of our alerts to a
// close says nothing about either.
type ConsoleFacts interface {
	// LocationConfirmed is whether the Console's target, last read, names
	// where this process reads the sets.
	LocationConfirmed() bool
	// KeyedByAlertID is whether the link keys this deployment's alerts by
	// the alert id this process sends, as of the last read that answered;
	// known is false until one has.
	KeyedByAlertID() (keyed bool, asOf time.Time, known bool)
	// KeyingAsked is whether the keying has been asked for the location in
	// force: until it has, an unknown keying is the round before the first
	// read, not a state to count.
	KeyingAsked() bool
	// Refresh asks the Console for whichever fact is due, on the copy's own
	// round: a replica that calibrates no strategy learns both all the same.
	// The implementation bounds how often it asks. Never called with the
	// copy's lock held.
	Refresh(ctx context.Context)
}

// ErrLocationUnconfirmed is a read of the sets refused because no location
// the Console named is bound: nothing is read from a place it did not name.
var ErrLocationUnconfirmed = errors.New("alarmd openalerts: the Console has not named where the open alert sets are")

// unconfirmed is why the sets cannot answer for this process: "" when both
// Console facts hold. A copy without Console facts is not configured, which
// is not a reason: see configured. Called with the lock held.
func (cache *Cache) unconfirmed() UnavailableReason {
	facts := cache.index.options.Facts
	if facts == nil {
		return ""
	}
	if !facts.LocationConfirmed() {
		return UnavailableLocationUnconfirmed
	}
	if keyed, _, known := facts.KeyedByAlertID(); !known || !keyed {
		return UnavailableKeyingUnconfirmed
	}
	return ""
}

// configured is whether the copy has a Console to take its facts from. One
// without (a deployment that does not use the link) reads nothing, is never
// unavailable and never degraded; its gate answers from what this process
// sent, as it would for an unconfirmed set.
func (cache *Cache) configured() bool {
	return cache.index.options.Facts != nil
}

// trusted is whether the sets may answer the gate: configured and both facts
// confirmed. Called with the lock held.
func (cache *Cache) trusted() bool {
	return cache.configured() && cache.unconfirmed() == ""
}

// noteConfirmation counts an entry into an unconfirmed state, once per
// entry: a copy that stays unconfirmed round after round counts nothing
// more. A keying not yet asked for the location in force - the rounds after
// a start or a move, before the first read - is not an entry yet. Called
// with the lock held after each step that asks the Console - the facts'
// refresh, each calibration - and at the end of every round.
func (cache *Cache) noteConfirmation() {
	reason := cache.unconfirmed()
	if reason == UnavailableKeyingUnconfirmed && !cache.index.options.Facts.KeyingAsked() {
		return
	}
	if reason != "" && reason != cache.index.lastUnconfirmed {
		cache.unavailable[reason]++
	}
	cache.index.lastUnconfirmed = reason
}
