// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package state

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// censusSchemaV1 is the shape of the stored record. It is inside the value
// rather than in the key: nothing reads a census yet except the planner that
// will be written with it (decision-020 section 4.7.3.1), so a shape change
// before then is a change to a record nobody is holding.
const censusSchemaV1 = "dimension-census-v1"

// PlanCensusKeyV2 names one Plan's dimension census. Plan level, like the gap
// marker and the no-data memory, and with no shard suffix: the census
// describes the strategy's series, which is what a split is planned from, and
// a piece's view of them is not a different fact about the strategy.
func PlanCensusKeyV2(prefix string, identity execution.PlanCensusIdentity) (string, error) {
	if err := validatePlanIdentity(prefix, identity.Plan, identity.StateGeneration); err != nil {
		return "", err
	}
	return executionKey(prefix, "census", identity.Plan, identity.StateGeneration, ""), nil
}

type censusEnvelope struct {
	Schema string                    `json:"schema"`
	Census execution.DimensionCensus `json:"census"`
}

// WriteCensus stores one Plan's census. It is a plain replace: the census is
// a reading of one round, not an accumulated memory, so there is nothing to
// merge and nothing another writer could be holding - and it is written at
// the TTL floor, renewed by the next round that takes one, so a strategy that
// stops being a candidate stops having a census rather than keeping a stale
// one for ever.
func (store *ExecutionStore) WriteCensus(ctx context.Context, census execution.DimensionCensus) (execution.CensusWriteOutcome, error) {
	result := execution.CensusWriteOutcome{Limit: store.options.MaxValueBytes}
	if store == nil {
		return result, errors.New("state: execution store is required")
	}
	if err := census.Validate(); err != nil {
		result.Status, result.ReasonCode = execution.CensusRejected, execution.ReasonCode(contract.ReasonStateCorrupt)
		return result, nil
	}
	key, err := PlanCensusKeyV2(store.options.Prefix, census.Identity)
	if err != nil {
		result.Status, result.ReasonCode = execution.CensusRejected, execution.ReasonCode(contract.ReasonStateCorrupt)
		return result, nil
	}
	encoded, err := json.Marshal(censusEnvelope{Schema: censusSchemaV1, Census: census})
	result.Bytes = len(encoded)
	if err != nil || len(encoded) > store.options.MaxValueBytes {
		result.Status, result.ReasonCode = execution.CensusRejected, execution.ReasonCode(contract.ReasonStateBudgetExceeded)
		return result, nil
	}
	target, routeErr := store.options.Router.Route(census.Identity.Plan.TenantID, census.Identity.Plan.StrategyID)
	if routeErr != nil {
		result.Status, result.ReasonCode = execution.CensusRetryable, execution.ReasonCode(contract.ReasonRedisUnavailable)
		return result, nil
	}
	if err := target.Backend.SetMany(ctx, []BackendWrite{{Key: key, Value: encoded, TTL: store.options.MinTTL}}); err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		result.Status, result.ReasonCode = execution.CensusRetryable, execution.ReasonCode(contract.ReasonRedisUnavailable)
		return result, nil
	}
	result.Status = execution.CensusWritten
	return result, nil
}

// ReadCensus reads one Plan's census. Written for the split planner, which
// lands with the dry-run batch; a missing key is not an error, it is a Plan
// nobody has taken a census of.
func (store *ExecutionStore) ReadCensus(ctx context.Context, identity execution.PlanCensusIdentity) (execution.DimensionCensus, bool, error) {
	key, err := PlanCensusKeyV2(store.options.Prefix, identity)
	if err != nil {
		return execution.DimensionCensus{}, false, err
	}
	target, routeErr := store.options.Router.Route(identity.Plan.TenantID, identity.Plan.StrategyID)
	if routeErr != nil {
		return execution.DimensionCensus{}, false, routeErr
	}
	values, err := target.Backend.MGet(ctx, []string{key})
	if err != nil {
		return execution.DimensionCensus{}, false, err
	}
	if len(values) != 1 || values[0] == nil {
		return execution.DimensionCensus{}, false, nil
	}
	var envelope censusEnvelope
	if err := json.Unmarshal(values[0], &envelope); err != nil || envelope.Schema != censusSchemaV1 {
		// A record this build cannot read is not a census. Reported as absent
		// rather than as an error: the planner's answer for "no census" is
		// already "do not split", which is the safe reading of bytes it does
		// not understand.
		return execution.DimensionCensus{}, false, nil
	}
	envelope.Census.Identity = identity
	if err := envelope.Census.Validate(); err != nil {
		return execution.DimensionCensus{}, false, nil
	}
	return envelope.Census, true, nil
}
