// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package access

import (
	"context"
	"errors"
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// KeptRead is a later read of one Slot's frozen physical queries, kept for a
// supplement of the Slot: the series of each query that the supplement is
// to evaluate, as the provider delivered them.
//
// It is what a supplement runs on instead of a query. The supplement is
// decided from the same read that found its series late, so the query
// service is asked once for both, and the read is replayed through the same
// series admission a first read goes through.
type KeptRead interface {
	// Replay delivers the kept series of one physical query, in the order
	// they were read, and returns the query's completion restated for what
	// it delivered: its delivery accumulated over those series alone, or
	// empty when it kept none. An error means the query was not kept.
	Replay(ctx context.Context, spec execution.PhysicalQuerySpec, sink execution.ProviderSeriesSink) (execution.ProviderCompletion, error)
}

type keptReadKey struct{}

// WithKeptRead gives a supplement execution the read it runs on.
func WithKeptRead(ctx context.Context, read KeptRead) context.Context {
	return context.WithValue(ctx, keptReadKey{}, read)
}

// KeptReadOf is the read WithKeptRead gave ctx, if any.
func KeptReadOf(ctx context.Context) (KeptRead, bool) {
	read, found := ctx.Value(keptReadKey{}).(KeptRead)
	return read, found && read != nil
}

// executeSupplement runs a supplement of a Slot on the read kept for it.
//
// Nothing here waits or queries: the Slot's data was ready when it was read,
// and it was read by the lookback under its own permits. So there is no
// readiness wait, no query permit and no recovery channel, and the read is
// not a first read the lookback could take. What is the same as a Slot's
// execution is what decides what the worker receives: the frozen plan of the
// Slot, planned as the Slot planned it - a consumer whose frozen budget ends
// before its data is ready was set aside by the Slot and is set aside here,
// since a supplement decides nothing the Slot would not have - and the
// series admission of every Plan's target. The admission and series-pulled
// counters are not fed: they count what the Slots of this process read, and
// a replay reads nothing.
func (source *Source) executeSupplement(
	ctx context.Context,
	request execution.QueryExecutionRequest,
	consumer execution.QueryExecutionConsumer,
) (execution.QueryExecutionCompletion, error) {
	read, found := KeptReadOf(ctx)
	if !found {
		return execution.QueryExecutionCompletion{}, errors.New("alarmd access: a supplement runs on a kept read, and none was given")
	}
	frozen, err := source.plans.ResolveFrozenPlan(ctx, request.Contract)
	if err != nil {
		return execution.QueryExecutionCompletion{}, fmt.Errorf("alarmd access: resolve frozen plan: %w", err)
	}
	prepared, err := Prepare(request.Contract, frozen, source.config.MinReadyDelay)
	if err != nil {
		return execution.QueryExecutionCompletion{}, err
	}
	if err := consumer.Begin(ctx, prepared.Header); err != nil {
		return execution.QueryExecutionCompletion{}, err
	}
	scopes := buildPlanScopes(prepared.Header.DuePlans, consumer.ResolvedTargets(), consumer.ResolvedScopeGroups())
	completion := execution.QueryExecutionCompletion{PhysicalQueries: make([]execution.PhysicalQueryCompletion, 0, len(prepared.Queries))}
	for _, query := range prepared.Queries {
		if len(query.Requirements) == 0 {
			completion = completeReadinessInvalidQuery(completion, query, request.AttemptNo)
			continue
		}
		adapter := &seriesAdapter{consumer: consumer, query: query, attemptNo: request.AttemptNo,
			admission: source.config.Admission, scopes: scopes}
		replayed, err := read.Replay(ctx, query.Spec, adapter)
		if err != nil {
			return execution.QueryExecutionCompletion{}, fmt.Errorf("alarmd access: replay kept read: %w", err)
		}
		if !trustedProviderCompletion(query.Spec.Digest, replayed) {
			return execution.QueryExecutionCompletion{}, errors.New("alarmd access: kept read returned an untrusted completion")
		}
		completion = appendQueryCompletion(completion, query, adapter.reconcileCompletion(replayed), request.AttemptNo)
	}
	completion.AllRequiredCompleted = true
	return completion, nil
}
