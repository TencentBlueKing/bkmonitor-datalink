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
//
// When the writer publishes when it published, the facts are aged from that
// instead, held to three of its publish rounds (five minutes each on the CMDB
// cache writer): one missed round is tolerated, and the index does not go in
// and out of stale with a single slow build; a writer that stopped is stale
// fifteen minutes after its last publish, not when its keys expire a week
// later.
const (
	cmdbIndexRefreshInterval         = time.Minute
	cmdbIndexStalenessBound          = 10 * cmdbIndexRefreshInterval
	cmdbIndexPublishedStalenessBound = 15 * time.Minute
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
	wait startupWaiter,
) (*admission.Chain, *cmdbcache.TenantStores, error) {
	// The platform states its key prefix once and both of its caches hang off
	// it, so the CMDB cache key comes from that one spelling; another
	// tenant's keys carry the tenant in front of it (cmdbcache.TenantStores).
	stores, err := cmdbcache.NewTenantStores(client, cfg.PlatformKeyPrefix(), cmdbcache.StoreOptions{
		RefreshInterval: cmdbIndexRefreshInterval,
		MaxAge:          cmdbIndexStalenessBound,
		PublishedMaxAge: cmdbIndexPublishedStalenessBound,
	})
	if err != nil {
		return nil, nil, err
	}
	store := stores.Default()
	// The first index is built before the worker can evaluate anything. A
	// worker that started without one would decide every scoped strategy's
	// series to be out of scope and silently stop alerting for them. This is
	// not a new dependency to fail on: the cache is on the database alarmd
	// already needs to run at all. A cache that does not answer is waited on
	// in place: the replica stays not ready, which is the same "no
	// evaluation without an index" this line exists for.
	if err := wait.await(ctx, "cmdb_index", store.Refresh); err != nil {
		return nil, nil, fmt.Errorf("alarmd: build the CMDB host index the target filter decides on: %w", err)
	}
	publishCMDBIndexHealth(recorder, store)

	filters := seriesAdmissionFilters(hostStatus, newIdentityReporter(logger, time.Now))
	recorder.SetHostDisableMonitorStates(hostDisableMonitorStateCount(filters))
	// Python's order: the record's own identities, then the host it names,
	// in the CMDB cache of the tenant the record is read for,
	// then the service instance it names - which may re-place it under the
	// instance's module and host.
	chain := admission.NewChain(
		[]admission.Fuller{
			admission.IdentityFuller{},
			cmdbcache.NewHostTopologyFuller(stores),
			cmdbcache.NewServiceInstanceTopologyFuller(stores),
		},
		filters,
	)
	return chain, stores, nil
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
//
// Every tenant's index is refreshed on the same tick.
func maintainCMDBIndex(ctx context.Context, stores *cmdbcache.TenantStores, recorder *metric.Recorder) {
	ticker := time.NewTicker(cmdbIndexRefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = stores.Refresh(ctx)
			publishCMDBIndexHealth(recorder, stores.Default())
		}
	}
}

func publishCMDBIndexHealth(recorder *metric.Recorder, store *cmdbcache.Store) {
	health := store.Health()
	recorder.SetCMDBHostIndex(
		health.Hosts, health.Age.Seconds(), health.PublishedAge.Seconds(), health.SourceAge.Seconds(), health.Degraded, health.DegradedReason,
	)
	recorder.SetCMDBServiceInstanceIndex(health.ServiceInstances)
	publishCMDBBusinessMappings(recorder, health)
	publishCMDBRefusedRecords(recorder, health)
}

// publishCMDBRefusedRecords names each count of what the held index's load
// refused by the label its gauge carries, for the same reason the mappings
// are named: the recorder would not notice two counts swapped.
func publishCMDBRefusedRecords(recorder *metric.Recorder, health cmdbcache.Health) {
	for record, refused := range map[string]int{
		"host": health.Refused.Hosts, "service_instance": health.Refused.ServiceInstances, "topo_node": health.Refused.TopoNodes,
	} {
		recorder.SetCMDBRecordsRefused(record, refused)
	}
}

// publishCMDBBusinessMappings names each business mapping the index holds by
// the label its gauge carries. The two mappings are read alike and neither
// the recorder nor the store would notice them published under each other's
// name.
func publishCMDBBusinessMappings(recorder *metric.Recorder, health cmdbcache.Health) {
	for mapping, stats := range map[string]cmdbcache.MappingStats{
		"bcs_cluster": health.ClusterBusinessMapping, "bcs_namespace": health.NamespaceBusinessMapping,
	} {
		recorder.SetCMDBBusinessMapping(mapping, stats.Held, stats.Refused, stats.Truncated, stats.ReadFailed, stats.Missing)
	}
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
//
// It is always in the chain, as Python installs it whatever the list
// (processor.py:76-80): a list emptied of states disables no host by state
// and still drops the invalid and unknown hosts (filters.py:114-116). Given
// none, it is built over no states.
func seriesAdmissionFilters(hostStatus *dynamicHostStatusFilter, reporter *admission.IdentityReporter) []admission.Filter {
	if hostStatus == nil {
		hostStatus = newDynamicHostStatusFilter(nil)
	}
	// The two target filters are told apart by which frozen form the Plan
	// carries; a Plan carries at most one, so at most one of them decides.
	return []admission.Filter{admission.TargetScopeFilter{Reporter: reporter}, admission.TargetPlanFilter{}, hostStatus}
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

// buildScopeGroups assembles what a target scope's dynamic group conditions
// are read from: the platform's dynamic group hash of each tenant, under the
// platform prefix the host index reads (cmdbcache.PlatformGroupStores), on
// the host index's cadence and staleness bound. There is no coordinate of its
// own: the platform keeps it beside the host hash. One read holds at most
// readBound bytes of group values, the bound the fork's group reader windows
// by.
func buildScopeGroups(cfg config.Config, client redis.Cmdable, readBound int) (*cmdbcache.PlatformGroupStores, error) {
	return cmdbcache.NewPlatformGroupStores(client, cfg.PlatformKeyPrefix(), cmdbcache.GroupStoreOptions{
		RefreshInterval: cmdbIndexRefreshInterval, MaxAge: cmdbIndexStalenessBound, ReadBound: readBound,
	})
}

// buildTargetResolver assembles what resolves a target plan's dynamic
// references (decision-017): the dynamic group store, when the deployment
// renders the fork's key prefix, and the host index for topology nodes.
// Without the prefix there is no group store, and every dynamic group
// selector resolves unavailable by name rather than empty; topology
// references still resolve against the host index.
//
// The group store reads its configured target group connection and refreshes the
// referenced groups on the host index's cadence with its staleness bound. One
// read holds at most readBound bytes of group documents at once: the
// timeline cache's bound, derived from the container
// (config.DeriveControlTimelineCache).
func buildTargetResolver(cfg config.Config, client redis.Cmdable, hosts cmdbcache.StoreSource, readBound int) (*cmdbcache.TargetResolver, *cmdbcache.GroupStore, error) {
	prefix, rendered := cfg.DynamicGroupKeyPrefix()
	if !rendered {
		return cmdbcache.NewTargetResolver(nil, hosts, time.Now), nil, nil
	}
	reader, err := cmdbcache.NewGroupReader(client, prefix)
	if err != nil {
		return nil, nil, err
	}
	groups, err := cmdbcache.NewGroupStore(reader, cmdbcache.GroupStoreOptions{
		RefreshInterval: cmdbIndexRefreshInterval, MaxAge: cmdbIndexStalenessBound, ReadBound: readBound,
	})
	if err != nil {
		return nil, nil, err
	}
	return cmdbcache.NewTargetResolver(groups, hosts, time.Now), groups, nil
}
