// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/admission"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/cmdbcache"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// How often the CMDB host index is rebuilt, and how old it may get before the
// process reports that it is deciding on stale facts.
//
// These are program decisions, not deployment ones. The cadence follows what
// the facts are: bk-monitor-worker refreshes the cache from CMDB by resource
// watch, so a host moving between topology nodes lands there within seconds and
// a minute of extra lag on top of it changes nothing an operator could tune
// better. The staleness bound is ten times the cadence, which is long enough
// that several failed reads in a row are not called stale and short enough that
// a cache which stopped being refreshed is visible well before an operator
// would notice through alerts.
const (
	cmdbIndexRefreshInterval = time.Minute
	cmdbIndexStalenessBound  = 10 * cmdbIndexRefreshInterval
)

// How often one plan may describe its object-identity rejections in the log.
// The counter carries the volume; the line carries the two sides that
// disagreed, and one such line per plan per minute is enough to read the
// mismatch off and few enough not to be the log.
const identityReportWindow = time.Minute

// buildSeriesAdmission assembles the access-path decision: enrich a series with
// the CMDB facts its strategy filters on, then admit it only inside that
// strategy's monitoring target.
//
// It reads the platform's CMDB host cache through the runtime client alarmd
// already holds - the cache lives on the same database - so the filter adds a
// reader rather than a connection, and there is no new coordinate for a
// deployment to state or to get wrong.
func buildSeriesAdmission(
	ctx context.Context,
	cfg config.Config,
	client redis.Cmdable,
	recorder *metric.Recorder,
	logger *observability.Logger,
	hostStatus *dynamicHostStatusFilter,
) (*admission.Chain, *cmdbcache.Store, error) {
	// The platform states its key prefix once and both of its caches hang off
	// it, so the CMDB cache key comes from that one spelling.
	prefix := cfg.PlatformKeyPrefix()
	reader, err := cmdbcache.NewReader(client, prefix)
	if err != nil {
		return nil, nil, err
	}
	store, err := cmdbcache.NewStore(reader, cmdbcache.StoreOptions{
		RefreshInterval: cmdbIndexRefreshInterval,
		MaxAge:          cmdbIndexStalenessBound,
	})
	if err != nil {
		return nil, nil, err
	}
	// The first index is built before the worker can evaluate anything. A
	// worker that started without one would decide every scoped strategy's
	// series to be out of scope and silently stop alerting for them. This is
	// not a new dependency to fail on: the cache is on the database alarmd
	// already needs to run at all.
	if err := store.Refresh(ctx); err != nil {
		return nil, nil, fmt.Errorf("alarmd: build the CMDB host index the target filter decides on: %w", err)
	}
	publishCMDBIndexHealth(recorder, store)

	filters := seriesAdmissionFilters(hostStatus, newIdentityReporter(logger, time.Now))
	recorder.SetHostDisableMonitorStates(hostDisableMonitorStateCount(filters))
	// Python's order: the record's own identities, then the host it names,
	// then the service instance it names - which may re-place it under the
	// instance's module and host.
	chain := admission.NewChain(
		[]admission.Fuller{
			admission.IdentityFuller{},
			cmdbcache.NewHostTopologyFuller(store),
			cmdbcache.NewServiceInstanceTopologyFuller(store),
		},
		filters,
	)
	return chain, store, nil
}

// newIdentityReporter writes one plan's object-identity rejections as a log
// line with the two sides that disagreed, at most once per plan, reason and
// window. Both sides are coordinates - dimension names, and the model and
// instance identifiers a target and a record name each other by - which the
// log envelope admits; no metric value or payload reaches the line.
func newIdentityReporter(logger *observability.Logger, now func() time.Time) *admission.IdentityReporter {
	if logger == nil {
		return nil
	}
	return admission.NewIdentityReporter(now, identityReportWindow, func(report admission.IdentityReport) {
		attributes := []slog.Attr{
			slog.String("reason", report.Reason),
			slog.String("bk_tenant_id", report.TenantID),
			slog.String("bk_biz_id", report.BusinessID),
			slog.String("strategy_id", report.StrategyID),
			slog.Uint64("rejections", report.Count),
		}
		if len(report.ExpectedPairs) > 0 {
			attributes = append(attributes,
				slog.String("expected_dimension_pairs", identityPairsText(report.ExpectedPairs)),
				slog.String("record_dimensions", strings.Join(report.DimensionNames, ",")),
			)
		}
		if len(report.CandidateKeys) > 0 || len(report.TargetKeys) > 0 {
			attributes = append(attributes,
				slog.String("record_keys", strings.Join(report.CandidateKeys, ",")),
				slog.String("target_keys_sample", strings.Join(report.TargetKeys, ",")),
			)
		}
		logger.Info("series_admission", "rejected", int(report.Count), 0, attributes...)
	})
}

func identityPairsText(pairs [][2]string) string {
	parts := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		parts = append(parts, pair[0]+"|"+pair[1])
	}
	return strings.Join(parts, ",")
}

// maintainCMDBIndex keeps the index fresh for as long as the process runs and
// republishes what the filter is deciding on after every attempt.
//
// A failed refresh keeps the previous index rather than dropping the filter:
// losing it would restore alerting outside every strategy's target, which is
// the defect this exists to fix, and refusing to evaluate would silence real
// alerts because a cache hiccuped. Staleness is reported instead of acted on.
func maintainCMDBIndex(ctx context.Context, store *cmdbcache.Store, recorder *metric.Recorder) {
	ticker := time.NewTicker(cmdbIndexRefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = store.Refresh(ctx)
			publishCMDBIndexHealth(recorder, store)
		}
	}
}

func publishCMDBIndexHealth(recorder *metric.Recorder, store *cmdbcache.Store) {
	health := store.Health()
	recorder.SetCMDBHostIndex(
		health.Hosts, health.Age.Seconds(), health.SourceAge.Seconds(), health.Degraded, health.DegradedReason,
	)
	recorder.SetCMDBServiceInstanceIndex(health.ServiceInstances)
}

// seriesAdmissionFilters is the access-path filter chain, in Python's order:
// the monitoring target decides whether the series belongs to the strategy
// at all, then the host's operational state decides whether it may alert.
//
// The host status filter decides on the states the platform settings copy
// answers: the platform's publication where there is one, the deployment's
// own layer beneath it, the platform's code default beneath that -- the
// same resolution the platform's own consumers apply, so the list in force
// here is the list in force there. It follows the copy when the platform
// changes it, through the filter's own swap, without a restart.
func seriesAdmissionFilters(hostStatus *dynamicHostStatusFilter, reporter *admission.IdentityReporter) []admission.Filter {
	filters := []admission.Filter{admission.TargetScopeFilter{Reporter: reporter}}
	if hostStatus != nil {
		filters = append(filters, hostStatus)
	}
	return filters
}

// hostDisableMonitorStateCount reports what the host status filter is actually
// deciding on, or zero when it is not installed. It reads the built filter
// rather than the configuration so the published number cannot disagree with
// the one in force.
func hostDisableMonitorStateCount(filters []admission.Filter) int {
	for _, filter := range filters {
		if hostStatus, ok := filter.(interface{ States() []string }); ok && filter.Name() == "host_status" {
			return len(hostStatus.States())
		}
	}
	return 0
}
