// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package redisfailure

import "context"

// Callers, closed: the jobs that share one Redis client and whose failures
// have to be told apart. A client's failures by reason say how often and why,
// and not whose - on the diagnostics client, which several observation jobs
// share on one small pool, a timeout could be a directory answer that came
// back short, a diagnostic record that was never written, or a cost
// projection that published nothing, and those lose different things.
const (
	// CallerDirectoryRead is a read the strategy directory makes for an
	// answer: the activation, the objects of the rows it returns, one Plan's
	// effective content or output. The directory keeps no copy of its own
	// and has no periodic read.
	CallerDirectoryRead = "directory_read"
	// CallerDiagnosticWrite is the diagnostic store writing an observation
	// window's records and series samples.
	CallerDiagnosticWrite = "diagnostic_write"
	// CallerDiagnosticRead is the diagnostic store reading them back.
	CallerDiagnosticRead = "diagnostic_read"
	// CallerCostProjection is the cost refresh: the replica registry it pages,
	// the other replicas' projections it loads, and its own it publishes.
	CallerCostProjection = "cost_projection"
)

// The jobs that share the source and runtime connections when a deployment
// points them at one Redis, each named where the bundle hands it its client.
const (
	// CallerStrategySource is the strategy cache the control plane reads.
	CallerStrategySource = "strategy_source"
	// CallerLegacyEffectiveTime is the strategy and host facts the absent-
	// strategy close and the effective-time compatibility read.
	CallerLegacyEffectiveTime = "legacy_effective_time"
	// CallerControlPlane is the published catalog: publication, activation,
	// timelines and objects, for the control rounds and for every Slot.
	CallerControlPlane = "control_plane"
	// CallerOwnership is leases, registrations and the fence.
	CallerOwnership = "ownership"
	// CallerRuntimeState is the Runtime State store: state, progress, gap
	// markers, no-data memory.
	CallerRuntimeState = "runtime_state"
	// CallerQueryCooldown is the shared query cooldowns.
	CallerQueryCooldown = "query_cooldown"
	// CallerFleet is the fleet snapshots and observation windows.
	CallerFleet = "fleet"
	// CallerCMDBCache is the host and topology cache series admission reads.
	CallerCMDBCache = "cmdb_cache"
	// CallerTargetGroup is the dynamic target groups.
	CallerTargetGroup = "target_group"
	// CallerDynamicConfig is the platform settings.
	CallerDynamicConfig = "dynamic_config"
	// CallerLinkd is the alert link's facts when they ride the runtime
	// connection.
	CallerLinkd = "linkd"
	// CallerStoreCensus is the census of what the stores hold by key family
	// (package storecensus): the Control Leader's, every ten minutes.
	CallerStoreCensus = "store_census"
	// CallerLegacyOutput is the strategy snapshots the compatibility output
	// writes on the service Redis.
	CallerLegacyOutput = "legacy_output"
	// CallerLegacyPodCache is the Pod facts it reads from the Django cache.
	CallerLegacyPodCache = "legacy_pod_cache"
	// CallerEffectiveMaintenance is a Query Group's effective-time maintenance
	// run: its fence checks and whatever else it asks Redis, beside the
	// owner's detection rounds rather than counted with them.
	CallerEffectiveMaintenance = "effective_maintenance"
	// CallerLeaseConfirmation is a fence check made only to move a lease held
	// under a worker session on by its TTL: the view gate's early renewal and
	// a long Slot's output asking before it defers. The fence check a round
	// makes confirms the lease too, and stays the ownership store's.
	CallerLeaseConfirmation = "lease_confirmation"
	// CallerCatalogTimelineRenewal, CallerCatalogObjectRenewal and
	// CallerCatalogContextRenewal are the Control Leader's renewals of the
	// catalog's Schedule timelines, Query Group execution objects (with the
	// manifest) and output contexts, each on its own cadence: told apart so
	// what each costs is read rather than split from one control_plane count.
	CallerCatalogTimelineRenewal = "catalog_timeline_renewal"
	CallerCatalogObjectRenewal   = "catalog_object_renewal"
	CallerCatalogContextRenewal  = "catalog_context_renewal"
)

// Callers is every caller name, for callers that must enumerate them.
var Callers = []string{CallerDirectoryRead, CallerDiagnosticWrite, CallerDiagnosticRead, CallerCostProjection,
	CallerStrategySource, CallerLegacyEffectiveTime, CallerControlPlane, CallerOwnership, CallerRuntimeState, CallerQueryCooldown,
	CallerFleet, CallerCMDBCache, CallerTargetGroup, CallerDynamicConfig, CallerLinkd, CallerStoreCensus,
	CallerLegacyOutput, CallerLegacyPodCache, CallerEffectiveMaintenance, CallerLeaseConfirmation,
	CallerCatalogTimelineRenewal, CallerCatalogObjectRenewal, CallerCatalogContextRenewal}

type callerKey struct{}

// WithCaller names the job the Redis calls made with ctx belong to.
func WithCaller(ctx context.Context, caller string) context.Context {
	return context.WithValue(ctx, callerKey{}, caller)
}

// Caller is the job ctx names, "" when it names none. A name outside Callers
// reads as "other", so a caller's typo cannot mint a series.
func Caller(ctx context.Context) string {
	caller, _ := ctx.Value(callerKey{}).(string)
	if caller == "" {
		return ""
	}
	for _, known := range Callers {
		if caller == known {
			return caller
		}
	}
	return "other"
}
