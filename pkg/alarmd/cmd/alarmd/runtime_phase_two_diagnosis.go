// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// diagnosisProgressBatch bounds one progress read of a diagnosis page, so a
// page of a large deployment is several bounded reads, not one unbounded:
// one pipeline of a batched control read each (ownership.ControlReadBatch).
const diagnosisProgressBatch = ownership.ControlReadBatch

// diagnosisUniverse reads the source's active set the way the control plane
// lists it, and classifies a failure into a reason word: the response goes to
// a CLI session, and a dependency's address is not part of a reason.
func diagnosisUniverse(source controlplane.StrategySource) fleet.UniverseReader {
	return func(ctx context.Context) ([]string, error) {
		if source == nil {
			return nil, errors.New("SOURCE_NOT_WIRED")
		}
		ids, err := source.ActiveStrategyIDs(ctx)
		switch {
		case err == nil:
			return ids, nil
		case errors.Is(err, controlplane.ErrLegacySourceIncomplete):
			return nil, fmt.Errorf("SOURCE_INCOMPLETE: the active strategy set is absent or does not decode")
		case errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled):
			return nil, errors.New("SOURCE_READ_TIMEOUT")
		default:
			return nil, errors.New("SOURCE_UNREADABLE")
		}
	}
}

// progressBatchLoader is the one method a diagnosis and a restore read
// progress through: a batch, within what the caller admits.
type progressBatchLoader interface {
	LoadProgressWithin(context.Context, []execution.ProgressIdentity, func(uint64) bool) ([]execution.ProgressLoadResult, []error, int)
}

// diagnosisProgress reads the page's objects' persisted progress in bounded
// batches, under the request's context so a client that went away stops the
// reads, each record admitted by the observation memory line before it is
// read. An object whose own read failed is returned apart from one with no
// record; one the line had no room for is returned deferred, and so is every
// object after it, the line refusing whichever consumer asks next; a batch
// that failed as a whole fails the page's progress.
func diagnosisProgress(store progressBatchLoader, admit func(uint64) bool) fleet.ProgressReader {
	if store == nil {
		return nil
	}
	return func(ctx context.Context, queryGroups []string) (map[string]fleet.ProgressFacts, map[string]string, error) {
		found := make(map[string]fleet.ProgressFacts, len(queryGroups))
		unread := map[string]string{}
		for start := 0; start < len(queryGroups); {
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
			end := min(start+diagnosisProgressBatch, len(queryGroups))
			identities := make([]execution.ProgressIdentity, 0, end-start)
			for _, group := range queryGroups[start:end] {
				identities = append(identities, execution.ProgressIdentity{QueryGroup: execution.QueryGroupIdentity(group)})
			}
			results, errs, read := store.LoadProgressWithin(ctx, identities, admit)
			if read == 0 {
				for _, group := range queryGroups[start:] {
					unread[group] = fleet.ProgressDeferred
				}
				break
			}
			batchFailed := 0
			for index := 0; index < read; index++ {
				group := string(identities[index].QueryGroup)
				if errs[index] != nil {
					unread[group] = fleet.ProgressReadFailed
					batchFailed++
					continue
				}
				if results[index].Status != execution.ProgressFound || results[index].Progress == nil {
					continue
				}
				found[group] = fleet.ProgressFacts{LastFullSlot: int64(results[index].Progress.LastFullSlot),
					NextSlot: int64(results[index].Progress.NextSlot)}
			}
			if batchFailed == read {
				return nil, nil, errors.New(fleet.ProgressUnreadable)
			}
			start += read
		}
		return found, unread, nil
	}
}

// diagnosisForwardTimeout bounds a diagnosis page's hop to the Leader. The
// Leader reads the source's set and the fleet's snapshots on a diagnosis's
// first page, which is more than a standing's memory read; the channel's own
// request deadline (3 s) still bounds the whole page, so this stays inside it.
const diagnosisForwardTimeout = 2500 * time.Millisecond
