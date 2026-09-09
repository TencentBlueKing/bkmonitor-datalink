// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/admission"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/cmdbcache"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
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
) (*admission.Chain, *cmdbcache.Store, error) {
	// The platform key prefix is already stated once, for the strategy
	// snapshot the compatibility output reads. Deriving the CMDB cache key
	// from the same value keeps one spelling of one location.
	prefix := cfg.Kafka.LegacyAdapter.SnapshotPrefix
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

	chain := admission.NewChain(
		[]admission.Fuller{admission.IdentityFuller{}, cmdbcache.NewHostTopologyFuller(store)},
		[]admission.Filter{admission.TargetScopeFilter{}},
	)
	return chain, store, nil
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
}
