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
// filtered close is twenty seconds (alert-close.md, "筛选关闭共享管理 API
// 二十秒总预算", Linkd c5e2443a3), and the answer comes after it, so the
// client waits that and five more for the answer's transfer. A client that
// gave up sooner would cut off a request the link is still serving within
// its budget, and lose its answer.
const filterCloseHTTPTimeout = 25 * time.Second

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
		Warn: linkdUnsupportedWarning(logger, "filter_close"), Report: newFilterCloseReporter(logger, now),
		RequestBudget: filterCloseHTTPTimeout, ObserveRequest: func(request filterclose.Request) {
			recorder.ObserveLinkdFilterCloseRequest(request.DryRun, request.Result, request.Elapsed)
		}, ReportJob: newFilterCloseJobReporter(logger, now)})
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
		logger.Warn("linkd_filter_close_"+notice.Outcome, "attention", notice.Read, notice.Elapsed,
			slog.String("tenant", notice.Scope.TenantID), slog.String("strategy_id", notice.Scope.StrategyID),
			slog.String("decision", notice.Decision), slog.Int("expected", notice.Expected), slog.String("expect_source", notice.Source),
			slog.Int("allowed", notice.Allowed), slog.Int("matched", notice.Matched), slog.Int("read", notice.Read),
			slog.Int("held_since_last", suppressed))
	}
}

// filterCloseJobStage is the stage of the line a filtered close job writes
// at its end, its word as the result.
const filterCloseJobStage = "linkd_filter_close_job"

// filterCloseJobReportEvery is how often one strategy's job line is
// written while its word stays the same; a word that changed is written at
// once. The lines held back meanwhile are counted on the next.
const filterCloseJobReportEvery = 10 * time.Minute

// newFilterCloseJobReporter writes the line of a filtered close job's end
// (filterclose.Closer.Run): the strategy, the decision, the job's word, what
// its real closes answered - closed, failed (answered other than 200),
// refused (among those, refused and left open) - and how long it ran;
// held_since_last and closed_since_last are the lines held back since the
// strategy's last one and the alerts those closed, so a sum of closed and
// closed_since_last over the lines is every alert the jobs closed. The
// counts by outcome carry no strategy, and the routine lines are sampled
// per Query Group, so a job_incomplete or round_spent could not be placed by
// strategy. Held per strategy and decision while its word stays the same,
// for filterCloseJobReportEvery: the decisions of one strategy (an absent
// close, an edit, a scope close) are jobs of their own, and one held
// behind another's line of the same word would leave no line to say which
// it was.
//
// What it remembers is bounded by what is active, not by a fixed count:
// once filterCloseReportKeys (tenant, strategy, decision) entries are held,
// a line drops only the entries not written for two periods (20 minutes).
// The bound is thus the number of distinct strategy decisions that wrote a
// line within the last 20 minutes: past 4096 of those, the memory keeps
// growing with them, and falls back once they go quiet.
func newFilterCloseJobReporter(logger *observability.Logger, now func() time.Time) func(filterclose.JobResult) {
	if logger == nil {
		return nil
	}
	if now == nil {
		now = time.Now
	}
	reporter := &filterCloseJobReporter{logger: logger, now: now, last: map[string]filterCloseJobWritten{}}
	return reporter.report
}

// filterCloseJobReporter is newFilterCloseJobReporter's memory: per tenant,
// strategy and decision, the word its last line wrote, when, and the lines held
// since with what they closed.
type filterCloseJobReporter struct {
	logger *observability.Logger
	now    func() time.Time
	mu     sync.Mutex
	last   map[string]filterCloseJobWritten
}

type filterCloseJobWritten struct {
	word       string
	at         time.Time
	held       int
	heldClosed int
}

func (reporter *filterCloseJobReporter) report(result filterclose.JobResult) {
	at := reporter.now()
	key := result.Scope.TenantID + "\x00" + result.Scope.StrategyID + "\x00" + result.Decision
	reporter.mu.Lock()
	before, known := reporter.last[key]
	if known && before.word == result.Word && at.Sub(before.at) < filterCloseJobReportEvery {
		before.held++
		before.heldClosed += result.Closed
		reporter.last[key] = before
		reporter.mu.Unlock()
		return
	}
	if len(reporter.last) >= filterCloseReportKeys {
		// Only what has been quiet for two periods goes: an entry written
		// since is still holding a line back. More active entries than
		// filterCloseReportKeys are all kept (newFilterCloseJobReporter).
		for k, written := range reporter.last {
			if at.Sub(written.at) >= 2*filterCloseJobReportEvery {
				delete(reporter.last, k)
			}
		}
	}
	reporter.last[key] = filterCloseJobWritten{word: result.Word, at: at}
	reporter.mu.Unlock()
	reporter.logger.Info(filterCloseJobStage, result.Word, result.Closed, result.Elapsed,
		slog.String("tenant", result.Scope.TenantID), slog.String("strategy_id", result.Scope.StrategyID),
		slog.String("decision", result.Decision), slog.Int("closed", result.Closed), slog.Int("failed", result.Failed),
		slog.Int("refused", result.Refused), slog.Int("held_since_last", before.held), slog.Int("closed_since_last", before.heldClosed))
}

// observeCloseJob writes one strategy's filtered close job line, with how
// long the job ran (its runs within the round), on the counter's own
// reading of a send (sent, filterclose.Result.Sent: a real close whose
// items the link answered): a close sent is written under the stage's
// word for one, degraded where its word to wait on is neither done nor
// one that got somewhere (a part refused or failed beside the part that
// was sent); a close not sent is not written where nothing was to be sent
// or nothing went wrong - no word, settled (nothing matched), more to do
// (rederived, incomplete), refused past its expectation (its own notice
// line says so), filtered close unsupported - and is written send_failed
// for every other word, whether or not a request went out: the link
// refused or failed the whole request (link_busy, link_failed, the
// request's own refusal), and also a job that ended before its request
// on alarmd's side - its filter refused (filter_refused), its record or
// command store unreadable (record_unavailable, command_unavailable), or
// a target list too large to send (target_list_too_large).
func observeCloseJob(ctx context.Context, observer observability.Observer, stage observability.Stage, sentWord observability.ReasonCode,
	strategyID, word string, sent bool, elapsed time.Duration) {
	if observer == nil {
		return
	}
	result, reason := observability.Result(observability.ResultSuccess), sentWord
	switch next := filterclose.NextAfter(word); {
	case sent && (next == filterclose.NextSettled || next == filterclose.NextAgain || word == filterclose.OutcomeJobDoneRefused):
	case sent:
		result = observability.ResultDegraded
	case word == "", next == filterclose.NextSettled, next == filterclose.NextAgain,
		word == filterclose.OutcomeExpectExceeded, word == filterclose.OutcomeUnsupported:
		return
	default:
		result, reason = observability.ResultDegraded, "send_failed"
	}
	ctx = observability.ContextWithTraceFields(ctx, observability.TraceFields{StrategyID: strategyID})
	observer.Observe(ctx, observability.Observation{Component: observability.ComponentControlPlane, Stage: stage,
		Result: result, ReasonCode: reason, Duration: elapsed, DurationKnown: true, Counts: observability.Counts{Events: 1}})
}
