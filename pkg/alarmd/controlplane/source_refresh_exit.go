// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane

import (
	"context"
	"errors"
	"time"

	"github.com/go-redis/redis/v8"
)

// SourceRefreshExit names where a refresh round stopped. A round that fails
// leaves nothing behind but its error, and the runtime keeps the last good
// catalog and goes on; on a running deployment that went on for three
// releases with one counter increment and one log line to show for it, and
// the log line had scrolled out of reach. The exit is the part of the error
// that is bounded enough to count, so that a round failing the same way every
// thirty seconds is a rising series rather than a memory.
//
// Closed set. Each value is one return in Refresh or one call it makes on
// the source; other is the fallback for an error no return claimed, and a
// rising other is a return that was added without an exit.
type SourceRefreshExit string

const (
	// SourceRefreshExitNone: the round did not fail.
	SourceRefreshExitNone SourceRefreshExit = "none"
	// SourceRefreshExitChangeSignal: reading the source's change signal failed.
	SourceRefreshExitChangeSignal SourceRefreshExit = "change_signal"
	// SourceRefreshExitActiveSetRead: reading the active strategy set failed
	// at the store.
	SourceRefreshExitActiveSetRead SourceRefreshExit = "active_set_read"
	// SourceRefreshExitActiveSetMissing: the active strategy set is absent,
	// empty or not a list. The source has not published, or published
	// nothing.
	SourceRefreshExitActiveSetMissing SourceRefreshExit = "active_set_missing"
	// SourceRefreshExitActiveSetInvalidID: one element of the active set is
	// not a canonical positive integer, and the whole set is refused for it.
	// One bad element fails every round the same way until it is gone; it
	// has its own exit because that shape, one datum against the whole
	// deployment, is worth telling apart from the store being down.
	SourceRefreshExitActiveSetInvalidID SourceRefreshExit = "active_set_invalid_id"
	// SourceRefreshExitActiveSetDuplicate: the active set lists an identity
	// twice or lists an empty one. Same shape as the invalid id: one element,
	// whole set refused, every round.
	SourceRefreshExitActiveSetDuplicate SourceRefreshExit = "active_set_duplicate"
	// SourceRefreshExitDocuments: reading the strategy documents failed.
	SourceRefreshExitDocuments SourceRefreshExit = "documents"
	// SourceRefreshExitObservationUnstable: the active set changed while the
	// documents were being read, or the documents did not match the set.
	// Clears itself on the next round unless the publisher is rewriting
	// continuously.
	SourceRefreshExitObservationUnstable SourceRefreshExit = "observation_unstable"
	// SourceRefreshExitObservationID: the observation could not be digested.
	SourceRefreshExitObservationID SourceRefreshExit = "observation_id"
	// SourceRefreshExitLastGood: the latest publication or its audit could
	// not be loaded.
	SourceRefreshExitLastGood SourceRefreshExit = "last_good"
	// SourceRefreshExitBuildCatalog: compiling the observation into a Catalog
	// failed as a whole. A single strategy that cannot compile is a
	// disposition, not this.
	SourceRefreshExitBuildCatalog SourceRefreshExit = "build_catalog"
	// SourceRefreshExitRetainExecutable: keeping the runtime-executable part
	// of the Catalog failed.
	SourceRefreshExitRetainExecutable SourceRefreshExit = "retain_executable"
	// SourceRefreshExitObservationChanged: the Catalog came out under another
	// observation than the round read.
	SourceRefreshExitObservationChanged SourceRefreshExit = "observation_changed"
	// SourceRefreshExitValidateCatalog: the deployment's Catalog validator
	// refused the Catalog.
	SourceRefreshExitValidateCatalog SourceRefreshExit = "validate_catalog"
	// SourceRefreshExitActivation: the activation record could not be read.
	SourceRefreshExitActivation SourceRefreshExit = "activation"
	// SourceRefreshExitConfirmation: the confirmation key could not be
	// derived.
	SourceRefreshExitConfirmation SourceRefreshExit = "confirmation"
	// SourceRefreshExitCandidate: the pending candidate could not be read,
	// written or cleared.
	SourceRefreshExitCandidate SourceRefreshExit = "candidate"
	// SourceRefreshExitPublish: publishing the Catalog, or restoring the
	// publication the activation already names, failed.
	SourceRefreshExitPublish SourceRefreshExit = "publish"
	// SourceRefreshExitOther: an error no return claimed.
	SourceRefreshExitOther SourceRefreshExit = "other"
)

// SourceRefreshExits is every exit in the order a round can reach them, for
// a reader that pre-creates one series per exit so that zero is a reading.
var SourceRefreshExits = []SourceRefreshExit{
	SourceRefreshExitNone,
	SourceRefreshExitChangeSignal,
	SourceRefreshExitActiveSetRead,
	SourceRefreshExitActiveSetMissing,
	SourceRefreshExitActiveSetInvalidID,
	SourceRefreshExitActiveSetDuplicate,
	SourceRefreshExitDocuments,
	SourceRefreshExitObservationUnstable,
	SourceRefreshExitObservationID,
	SourceRefreshExitLastGood,
	SourceRefreshExitBuildCatalog,
	SourceRefreshExitRetainExecutable,
	SourceRefreshExitObservationChanged,
	SourceRefreshExitValidateCatalog,
	SourceRefreshExitActivation,
	SourceRefreshExitConfirmation,
	SourceRefreshExitCandidate,
	SourceRefreshExitPublish,
	SourceRefreshExitOther,
}

// ValidSourceRefreshExit reports whether exit is one of the closed set.
func ValidSourceRefreshExit(exit SourceRefreshExit) bool {
	for _, known := range SourceRefreshExits {
		if exit == known {
			return true
		}
	}
	return false
}

// SourceRefreshFailure is the error a failed refresh round returns: the
// cause, and the exit it stopped at. It unwraps to the cause, so every
// errors.Is a caller already does keeps working, and its text is the cause's
// text, so no log line changed by this.
type SourceRefreshFailure struct {
	Exit SourceRefreshExit
	Err  error
}

func (failure *SourceRefreshFailure) Error() string {
	if failure == nil || failure.Err == nil {
		return "alarmd controlplane: source refresh failed"
	}
	return failure.Err.Error()
}

func (failure *SourceRefreshFailure) Unwrap() error {
	if failure == nil {
		return nil
	}
	return failure.Err
}

// SourceRefreshExitOf names the exit a failed round stopped at, other for an
// error that came from nowhere Refresh claims, and none for no error.
func SourceRefreshExitOf(err error) SourceRefreshExit {
	if err == nil {
		return SourceRefreshExitNone
	}
	var failure *SourceRefreshFailure
	if errors.As(err, &failure) && failure != nil && ValidSourceRefreshExit(failure.Exit) &&
		failure.Exit != SourceRefreshExitNone {
		return failure.Exit
	}
	return SourceRefreshExitOther
}

// exitAt claims an error for an exit. An error already claimed by an inner
// step keeps that claim: the innermost step is the one that knows where the
// round stopped, and the outer return only knows that it did.
func exitAt(exit SourceRefreshExit, err error) error {
	if err == nil {
		return nil
	}
	var failure *SourceRefreshFailure
	if errors.As(err, &failure) {
		return err
	}
	return &SourceRefreshFailure{Exit: exit, Err: err}
}

// activeSetExit tells the three ways reading the active set fails apart.
// The source reports all of them as one incomplete-source error, which is
// right for what the round does next (nothing), and wrong for what a reader
// of the series does next: a store that is down and a list with one bad
// element in it are fixed by different people.
func activeSetExit(err error) SourceRefreshExit {
	switch {
	case errors.Is(err, ErrActiveStrategyIDInvalid):
		return SourceRefreshExitActiveSetInvalidID
	case errors.Is(err, ErrActiveSetNotCanonical):
		return SourceRefreshExitActiveSetDuplicate
	case errors.Is(err, ErrLegacySourceIncomplete):
		return SourceRefreshExitActiveSetMissing
	default:
		return SourceRefreshExitActiveSetRead
	}
}

// SourceStalenessBound is how stale the Catalog may be by design: the
// periodic full read guarantees a change the source made without moving its
// signal is seen within it. A source that has not refreshed successfully for
// longer than this is past the exposure the design accepts, whatever the
// reason, and the deployment's health says so.
const SourceStalenessBound = sourceFullReadInterval

// SourceRefreshSuccessMark is the persisted time of the last refresh round
// that returned without error, under any status: a round that found the
// source unchanged succeeded as surely as one that published.
//
// It is persisted, without expiry, so that its age is a difference between
// two facts that survive a restart: a process that starts against a source
// that has been failing for a day reads a day, not the seconds since its own
// start. An expiry would turn the reading absent exactly when it is largest,
// which is when it is needed.
func (repository *RedisCatalogRepository) sourceRefreshSuccessKey() string {
	return repository.prefix + ":source_refreshed_at"
}

// MarkSourceRefreshSuccess records that a refresh round succeeded at the
// given time.
func (repository *RedisCatalogRepository) MarkSourceRefreshSuccess(ctx context.Context, at time.Time) error {
	if repository == nil || repository.client == nil {
		return errors.New("alarmd controlplane: catalog repository is required")
	}
	if at.IsZero() {
		return errors.New("alarmd controlplane: source refresh success time is required")
	}
	return repository.client.Set(ctx, repository.sourceRefreshSuccessKey(), at.Unix(), 0).Err()
}

// LoadSourceRefreshSuccess reads when a refresh round last succeeded. The
// second result is false when no round ever has under this prefix, which is
// a different answer from any time.
func (repository *RedisCatalogRepository) LoadSourceRefreshSuccess(ctx context.Context) (time.Time, bool, error) {
	if repository == nil || repository.client == nil {
		return time.Time{}, false, errors.New("alarmd controlplane: catalog repository is required")
	}
	seconds, err := repository.client.Get(ctx, repository.sourceRefreshSuccessKey()).Int64()
	if errors.Is(err, redis.Nil) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	if seconds <= 0 {
		return time.Time{}, false, errors.New("alarmd controlplane: persisted source refresh success time is invalid")
	}
	return time.Unix(seconds, 0), true, nil
}
