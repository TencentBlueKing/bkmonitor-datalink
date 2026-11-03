// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/absentalerts"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/filterclose"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdmanage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openepisodes"
)

// Every close alarmd decides goes through the link's search and close by
// filter (package filterclose): the control leader's edit and absent
// decisions, and every owner's inactive and superseded-identity closes. A link
// that answers 501, 404 or 405 - storage that cannot search, or a link
// older than the routes - is filtered close unsupported: each decision
// meanwhile answers filterclose.OutcomeUnsupported, counted, and the
// answer is warned; nothing closes the decision another way.
//
// The leader's absent close loop runs the leader's work in its rounds: the
// absent decisions, then the edit close, then the retries of kept items.

// filterCloseHTTPTimeout bounds one request: the link's own budget for a
// filtered close is ten seconds (alert-close.md), plus the transfer.
const filterCloseHTTPTimeout = 15 * time.Second

// filterClosePendingPerRound is how many kept items a round retries.
const filterClosePendingPerRound = 500

// newFilterCloser is the closer for a deployment with the link's management
// client; nil otherwise.
func newFilterCloser(cfg config.Config, client *linkdmanage.Client, runtime redis.UniversalClient, now func() time.Time,
	recorder *metric.Recorder, logger *observability.Logger) (*filterclose.Closer, error) {
	if client == nil || runtime == nil {
		recorder.SetLinkdFilterCloseSource(filterclose.Outcomes, (*filterclose.Closer)(nil).Stats)
		return nil, nil
	}
	commands, err := filterclose.NewCommands(runtime, cfg.Redis.StatePrefix)
	if err != nil {
		return nil, err
	}
	pending, err := filterclose.NewPending(runtime, cfg.Redis.StatePrefix, filterclose.MaxPending)
	if err != nil {
		return nil, err
	}
	record, err := openepisodes.NewStore(runtime, cfg.Redis.StatePrefix)
	if err != nil {
		return nil, err
	}
	closer, err := filterclose.New(filterclose.Options{Link: client, Commands: commands, Pending: pending, Record: record, Now: now,
		Warn: linkdUnsupportedWarning(logger, "filter_close"), Report: newFilterCloseReporter(logger, now)})
	if err != nil {
		return nil, err
	}
	recorder.SetLinkdFilterCloseSource(filterclose.Outcomes, closer.Stats)
	return closer, nil
}

// newEpisodeWriter is the writer of alarmd's record of the episodes it
// opened, for a deployment with the link's management client (the record
// is only read before a close through it); nil otherwise. Its counts are
// bound either way, so the family reads zeros rather than nothing.
func newEpisodeWriter(cfg config.Config, client *linkdmanage.Client, runtime redis.UniversalClient, now func() time.Time,
	recorder *metric.Recorder) (*openepisodes.Writer, error) {
	if client == nil || runtime == nil {
		recorder.SetOpenEpisodeRecordSource(openepisodes.Outcomes, (*openepisodes.Writer)(nil).Stats)
		return nil, nil
	}
	store, err := openepisodes.NewStore(runtime, cfg.Redis.StatePrefix)
	if err != nil {
		return nil, err
	}
	writer, err := openepisodes.NewWriter(store, now)
	if err != nil {
		return nil, err
	}
	recorder.SetOpenEpisodeRecordSource(openepisodes.Outcomes, writer.Stats)
	return writer, nil
}

// filterRoute is the closer and the deployment's event source every filter
// names.
type filterRoute struct {
	closer *filterclose.Closer
	source string
}

func newFilterRoute(cfg config.Config, closer *filterclose.Closer) *filterRoute {
	if closer == nil {
		return nil
	}
	return &filterRoute{closer: closer, source: cfg.PhaseTwo.Linkd.ControlPlane.Source()}
}

func (route *filterRoute) scope(key absentalerts.Key) filterclose.Scope {
	return filterclose.Scope{TenantID: key.TenantID, EventSourceID: route.source, StrategyID: key.StrategyID}
}

// retryPending retries the kept items, on the leader.
func (route *filterRoute) retryPending(ctx context.Context) {
	if route == nil {
		return
	}
	round, cancel := context.WithTimeout(ctx, absentCloseInterval/4)
	defer cancel()
	route.closer.RetryPending(round, filterClosePendingPerRound)
}

// forget drops the term's memory when the term ends.
func (route *filterRoute) forget() {
	if route != nil {
		route.closer.Forget()
	}
}

// filterCloseReportEvery is how often one outcome of one strategy writes its
// line; the lines held back meanwhile are counted on the next.
const filterCloseReportEvery = 10 * time.Minute

// filterCloseReportKeys bounds the outcome-and-strategy pairs remembered;
// past it the ones not written for two periods go.
const filterCloseReportKeys = 4096

// newFilterCloseReporter writes a filtered close's notice -- a close
// refused past its expectation, a walk cut at its turn's bound -- with the
// strategy, the decision and the numbers that place it, sampled per outcome
// and strategy. The counts alone carry no strategy, so a refusal was found
// only by reading the absent roster.
func newFilterCloseReporter(logger *observability.Logger, now func() time.Time) func(filterclose.Notice) {
	if logger == nil {
		return nil
	}
	if now == nil {
		now = time.Now
	}
	var mu sync.Mutex
	last := map[string]time.Time{}
	held := map[string]int{}
	return func(notice filterclose.Notice) {
		at := now()
		key := notice.Outcome + "\x00" + notice.Scope.TenantID + "\x00" + notice.Scope.StrategyID
		mu.Lock()
		if written, ok := last[key]; ok && at.Sub(written) < filterCloseReportEvery {
			held[key]++
			mu.Unlock()
			return
		}
		if len(last) >= filterCloseReportKeys {
			for k, written := range last {
				if at.Sub(written) >= 2*filterCloseReportEvery {
					delete(last, k)
					delete(held, k)
				}
			}
		}
		suppressed := held[key]
		last[key] = at
		delete(held, key)
		mu.Unlock()
		logger.Warn("linkd_filter_close_"+notice.Outcome, "attention", notice.Read, 0,
			slog.String("tenant", notice.Scope.TenantID), slog.String("strategy_id", notice.Scope.StrategyID),
			slog.String("decision", notice.Decision), slog.Int("expected", notice.Expected), slog.String("expect_source", notice.Source),
			slog.Int("allowed", notice.Allowed), slog.Int("matched", notice.Matched), slog.Int("read", notice.Read),
			slog.Int("held_since_last", suppressed))
	}
}
