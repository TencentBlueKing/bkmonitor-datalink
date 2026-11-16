// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package state

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// noDataStageMarkerField names the attempt a staging key belongs to. It is
// outside the group namespace, so no group can be named it, and it is not
// the header, so a staging key is never mistaken for a record; the swap
// removes it before the key becomes one.
const noDataStageMarkerField = "_stage"

// The staging key's life. Every segment sets it again, so it only has to
// outlive the gap between two scripts of one write; what it bounds is how
// long a write abandoned part way - the worker gone, the Slot past its
// deadline - leaves its fields holding the server's memory. It is the time
// left to the write's own deadline, the Slot's that its context carries, with
// the floor beside it, so the key goes about when the attempt it belongs to
// can no longer finish. A write without a deadline, which only a test makes,
// takes the ceiling.
const (
	noDataStageTTLFloor   = 30 * time.Second
	noDataStageTTLCeiling = 10 * time.Minute
)

// errNoDataGroupEncoding is a group that could not be encoded, which is the
// statement's fault and not the server's.
var errNoDataGroupEncoding = errors.New("state: no-data group is not encodable")

// errNoDataStagedMemory is a whole record whose staged groups do not fold to
// the memory its statement states: the writer's fault, as a bad count is.
var errNoDataStagedMemory = errors.New("state: no-data staged groups are not the memory the statement states")

// errNoDataGroupCount is a whole-record statement whose group count is not
// the number of groups it carries, the statement's fault as well: the swap
// would find the staging key short or long on every attempt.
var errNoDataGroupCount = errors.New("state: no-data whole record counts other groups than it carries")

func noDataStageTTL(ctx context.Context, now time.Time) time.Duration {
	deadline, bounded := ctx.Deadline()
	if !bounded {
		return noDataStageTTLCeiling
	}
	return min(max(deadline.Sub(now), 0)+noDataStageTTLFloor, noDataStageTTLCeiling)
}

// noDataStageMarker is the value an attempt's staging key carries: the fence
// epoch and attempt number its name already has, and a nonce of this write.
// The nonce is what makes the marker this write's and not only this name's:
// two writes under one name - a caller that holds no fence, or one attempt
// writing the same Plan twice - each find the other's marker and stop rather
// than add fields to it.
func noDataStageMarker(attempt execution.NoDataWriteAttempt) ([]byte, error) {
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	return []byte(strconv.FormatUint(attempt.OwnerEpoch, 10) + ":" + strconv.FormatUint(uint64(attempt.AttemptNo), 10) +
		":" + hex.EncodeToString(nonce[:])), nil
}

// replaceNoDataRecord writes a whole memory into the attempt's staging key a
// segment at a time and then puts it in place of the formal key in one
// script, so a reader of the formal key sees the record it had or the new one
// whole, and never part of either.
//
// What it builds at a time is one segment's fields and arguments, not the
// memory's: the server is held one segment's script at a time, and so is the
// worker's memory beyond the statement itself, which the worker reserves at
// one segment (execution.NoDataFirstWriteSegmentGroups).
func (store *ExecutionStore) replaceNoDataRecord(
	ctx context.Context, backend NoDataHashBackend, key, stageKey string, mutation execution.PlanNoDataMutation,
	header, expectedHeader []byte, lifetime time.Duration,
	attempt execution.NoDataWriteAttempt,
) (HashDeltaOutcome, error) {
	// The swap's HLEN is the distinct groups and the marker. Set is strictly
	// ordered (execution.ValidatePlanNoDataMutation), so its length is its
	// distinct groups, and a statement derived from no record carries every
	// group, so that is GroupCount: the two are one number, bound here before
	// anything is staged.
	//
	// Groups staged ahead of the statement (mutation.Staged) are in the key
	// already, under their write's marker; Set is the rest, and the two are
	// GroupCount together.
	staged := mutation.Staged
	if uint64(mutation.GroupCount) != staged.Groups()+uint64(len(mutation.Set)) {
		return HashDeltaOutcome{}, errNoDataGroupCount
	}
	marker := []byte(nil)
	if staged != nil {
		if staged.PresentAsOf != mutation.PresentAsOf {
			return HashDeltaOutcome{}, errNoDataStagedMemory
		}
		marker = staged.Marker
	} else {
		var err error
		if marker, err = noDataStageMarker(attempt); err != nil {
			return HashDeltaOutcome{}, err
		}
	}
	memory := staged.Fold()
	ttl := noDataStageTTL(ctx, store.options.Now())
	for start := 0; start < len(mutation.Set) || start == 0 && staged == nil; start += execution.NoDataFirstWriteSegmentGroups {
		end := min(start+execution.NoDataFirstWriteSegmentGroups, len(mutation.Set))
		fields, encodeErr := encodeNoDataGroups(mutation.Set[start:end])
		if encodeErr != nil {
			return HashDeltaOutcome{}, errors.Join(errNoDataGroupEncoding, encodeErr)
		}
		if foldErr := foldNoDataValues(&memory, mutation.Set[start:end], mutation.PresentAsOf); foldErr != nil {
			return HashDeltaOutcome{}, errors.Join(errNoDataGroupEncoding, foldErr)
		}
		outcome, stageErr := backend.StageHashSegment(ctx, HashStageSegment{
			Key: stageKey, MarkerField: noDataStageMarkerField, Marker: marker,
			First: start == 0 && staged == nil, Set: fields, TTL: ttl,
		})
		if stageErr != nil || outcome.Status != HashDeltaApplied {
			return outcome, stageErr
		}
	}
	// What is staged is the memory the statement states, folded here from
	// the values written and apart by the writer: a group staged twice, or
	// one missing from either, is a different digest, and nothing is put in
	// place. The key goes with its TTL.
	if digest, digestErr := memory.MemoryDigest(mutation); digestErr != nil || digest != mutation.MemoryDigest {
		return HashDeltaOutcome{}, errNoDataStagedMemory
	}
	swap := HashStageSwap{
		Key: key, StageKey: stageKey, HeaderField: noDataHeaderField,
		ExpectedMissing: expectedHeader == nil, Header: header,
		MarkerField: noDataStageMarkerField, Marker: marker,
		// The groups the memory holds, as its writer counted them, which is
		// a count of distinct groups and not of fields sent: a group two
		// segments both carry is one field. len(Set), as bound above.
		Fields: int(mutation.GroupCount) + 1,
		TTL:    lifetime,
	}
	if expectedHeader != nil {
		swap.ExpectedDigest = HeaderDigest(expectedHeader)
	}
	return backend.SwapStagedHash(ctx, swap)
}

// foldNoDataValues folds staged values into memory as the memory they are of,
// against the record's present-as-of.
func foldNoDataValues(memory *execution.NoDataMemoryDigestAccumulator, values []execution.NoDataGroupDelta, presentAsOf int64) error {
	for _, value := range values {
		if err := memory.Add(execution.NoDataGroupMemoryOfValue(value, presentAsOf)); err != nil {
			return err
		}
	}
	return nil
}

// StageNoData writes one segment of a Plan's whole memory into the attempt's
// staging key ahead of its statement (execution.PlanNoDataStager): the first
// starts the key over under a new marker, every later one proves it.
func (store *ExecutionStore) StageNoData(
	ctx context.Context, request execution.NoDataStageRequest,
) (execution.NoDataStageResult, error) {
	reject := func(reason string) (execution.NoDataStageResult, error) {
		return execution.NoDataStageResult{Status: execution.NoDataRejected, ReasonCode: execution.ReasonCode(reason)}, nil
	}
	retry := func(reason string) (execution.NoDataStageResult, error) {
		return execution.NoDataStageResult{Status: execution.NoDataRetryable, ReasonCode: execution.ReasonCode(reason)}, nil
	}
	if err := request.Contract.Validate(); err != nil {
		return execution.NoDataStageResult{}, fmt.Errorf("state: invalid no-data stage request: %w", err)
	}
	if err := execution.ValidateNoDataStageSegment(request); err != nil {
		return reject(contract.ReasonStateCorrupt)
	}
	backend, stageKey, err := store.noDataStageBackend(request.Identity, request.Attempt)
	if err != nil {
		return reject(contract.ReasonStateCorrupt)
	}
	if backend == nil {
		return retry(contract.ReasonRedisUnavailable)
	}
	fields, err := encodeNoDataGroups(request.Set)
	if err != nil {
		return reject(contract.ReasonStateCorrupt)
	}
	memory := request.Staged.Fold()
	if err := foldNoDataValues(&memory, request.Set, request.PresentAsOf); err != nil {
		return reject(contract.ReasonStateCorrupt)
	}
	marker := []byte(nil)
	if request.Staged != nil {
		marker = request.Staged.Marker
	} else if marker, err = noDataStageMarker(request.Attempt); err != nil {
		return retry(contract.ReasonRedisUnavailable)
	}
	outcome, err := backend.StageHashSegment(ctx, HashStageSegment{
		Key: stageKey, MarkerField: noDataStageMarkerField, Marker: marker,
		First: request.Staged == nil, Set: fields, TTL: noDataStageTTL(ctx, store.options.Now()),
	})
	switch {
	case err != nil:
		return retry(contract.ReasonRedisUnavailable)
	case outcome.Status != HashDeltaApplied:
		// Lost between two segments, or started over under its name.
		return retry(contract.ReasonStateWriteRetryable)
	}
	return execution.NoDataStageResult{Status: execution.NoDataApplied,
		Staged: execution.NewNoDataStaged(marker, request.PresentAsOf, memory)}, nil
}

// DiscardNoDataStage deletes an attempt's staging key when it still holds
// this write's marker (execution.PlanNoDataStager).
func (store *ExecutionStore) DiscardNoDataStage(ctx context.Context, discard execution.NoDataStageDiscard) error {
	if discard.Staged == nil {
		return nil
	}
	backend, stageKey, err := store.noDataStageBackend(discard.Identity, discard.Attempt)
	if err != nil || backend == nil {
		return errors.Join(errors.New("state: no-data staging key not reachable"), err)
	}
	return backend.DiscardStagedHash(ctx, stageKey, noDataStageMarkerField, discard.Staged.Marker)
}

// noDataStageBackend is the backend and staging key of one attempt's write of
// a Plan's whole memory; a nil backend is one the router cannot reach now.
func (store *ExecutionStore) noDataStageBackend(
	identity execution.PlanNoDataIdentity, attempt execution.NoDataWriteAttempt,
) (NoDataHashBackend, string, error) {
	stageKey, err := PlanNoDataStageKeyV1(store.options.Prefix, identity, attempt)
	if err != nil {
		return nil, "", err
	}
	target, err := store.options.Router.Route(identity.Plan.TenantID, identity.Plan.StrategyID)
	if err != nil {
		return nil, stageKey, nil
	}
	backend, ok := target.Backend.(NoDataHashBackend)
	if !ok {
		return nil, "", errors.New("state: the backend cannot stage a no-data record")
	}
	return backend, stageKey, nil
}
