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
	"log/slog"
	"net/http"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/activecopy"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdmanage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/newopens"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openepisodes"
)

// The link's control plane is the one way alarmd reads or closes the link's
// alerts: its search and its close by filter, called with the management
// Internal-Token (linkdmanage). The recovery gate and the no-data end read
// alarmd's own copy, refreshed by fingerprint through the search
// (activecopy); every close the leader or an owner decides goes through the
// filter (filterclose). A deployment that names no control plane reads and
// closes nothing at the link, and says so at startup; a link that answers
// 501 is filtered close unsupported, counted and warned, with no other path.

// effectiveTimeFailureLine is the warning a failing effective-time refresh
// writes, once per failure class change.
func effectiveTimeFailureLine(logger *observability.Logger) func(dependency, class string, failures uint64) {
	return func(dependency, class string, failures uint64) {
		logger.Warn("effective_time_refresh_failed", "failed", 0, 0, slog.String("dependency", dependency),
			slog.String("class", class), slog.Uint64("failures", failures))
	}
}

// newLinkdManage is the link's management client for a deployment that
// names the control plane and its secret; nil otherwise.
func newLinkdManage(cfg config.Config, now func() time.Time) (*linkdmanage.Client, error) {
	plane := cfg.PhaseTwo.Linkd.ControlPlane
	if !plane.Configured() {
		return nil, nil
	}
	return linkdmanage.New(linkdmanage.Options{BaseURL: plane.URL, Secret: plane.JWTSecret, Username: plane.Username(),
		Client: &http.Client{Timeout: filterCloseHTTPTimeout}, Now: now})
}

// linkdUnsupportedWarning is the line written when the link answers that it
// cannot search or close by filter (501, or 404/405 from a link older than
// the routes): nothing reads or closes the link's alerts until it can.
func linkdUnsupportedWarning(logger *observability.Logger, part string) func(error) {
	return func(err error) {
		if logger == nil {
			return
		}
		logger.Warn("linkd_filter_close_unsupported", "degraded", 0, 0, slog.String("part", part),
			slog.String("error", err.Error()))
	}
}

// linkdRefusedWarning is the line written when the link starts refusing
// alarmd's credential (401, 403): nothing is read from the link until the
// secret is the link's again. Once per run of refusals.
func linkdRefusedWarning(logger *observability.Logger, part string) func(error) {
	return func(err error) {
		if logger == nil {
			return
		}
		logger.Warn("linkd_credential_refused", "degraded", 0, 0, slog.String("part", part),
			slog.String("error", err.Error()))
	}
}

// warnLinkdUnconfigured writes, at startup, that a deployment whose alerts
// do not go to the link (no phase_two.linkd block; one with the block does
// not load without its control plane) reads and closes nothing there, and
// that its recovery gate answers from the alerts this process opened.
func warnLinkdUnconfigured(cfg config.Config, logger *observability.Logger) {
	if logger == nil || cfg.PhaseTwo.Linkd.ControlPlane.Configured() {
		return
	}
	logger.Warn("linkd_control_plane_unconfigured", "degraded", 0, 0,
		slog.String("detail", "no phase_two.linkd block: alerts are not read back from or closed at the link; the recovery gate "+
			"passes a RECOVERY only for an alert this process opened, and no edit, absent, inactive or scope close is made"))
}

// newActiveCopy is the copy for a deployment with the link's management
// client; nil otherwise.
func newActiveCopy(cfg config.Config, client *linkdmanage.Client, now func() time.Time, logger *observability.Logger) (*activecopy.Copy, error) {
	if client == nil {
		return nil, nil
	}
	return activecopy.New(activecopy.Options{Link: client, EventSourceID: cfg.PhaseTwo.Linkd.ControlPlane.Source(), Now: now,
		Warn: linkdUnsupportedWarning(logger, "active_copy"), Refused: linkdRefusedWarning(logger, "active_copy")})
}

// openAlertCopyPort adapts the copy to the worker's port: the worker speaks
// in Plan identities, the copy in strategy keys. A nil copy is a deployment
// whose alerts do not go to the link (no phase_two.linkd block; one with the
// block does not load without the control plane): never linked, so the
// worker keeps no no-data end index, and the gate answers from own, the
// record of the alerts this process opened, as it did before the link copy
// existed.
type openAlertCopyPort struct {
	copy *activecopy.Copy
	// own answers the gate when there is no copy; nil beside a copy.
	own *activecopy.Own
	// registerOwned also tells the effective-time maintenance which Plans
	// this process evaluates; nil in tests that need only the copy.
	registerOwned func(execution.QueryGroupIdentity, []execution.PlanIdentity)
	// record hears what each acknowledged batch opened and ended, as the
	// copy judges it (openepisodes); nil without the link's control plane.
	record *openepisodes.Writer
	// opens counts each strategy's opens in the hour, as the copy judges
	// them (newopens); nil in tests that need only the copy.
	opens *newopens.Counter
}

// Linked says whether a link stands behind the copy (execution.OpenAlertLink).
func (port openAlertCopyPort) Linked() bool { return port.copy.Linked() }

// Holds is the copy's judged membership (execution.OpenAlertJudge).
func (port openAlertCopyPort) Holds(tenantID, strategyID, fingerprint string) (bool, bool) {
	return port.copy.Holds(tenantID, strategyID, fingerprint)
}

// Contains is the recovery gate's question: the link copy's answer where
// the link is the destination, the record of this process's own opens
// where it is not.
func (port openAlertCopyPort) Contains(tenantID, strategyID, fingerprint string) bool {
	if port.copy == nil {
		return port.own.Contains(tenantID, strategyID, fingerprint)
	}
	return port.copy.Contains(tenantID, strategyID, fingerprint)
}

func (port openAlertCopyPort) TrackPlans(qg execution.QueryGroupIdentity, plans []execution.PlanIdentity) {
	keys := make([]activecopy.StrategyKey, 0, len(plans))
	for _, plan := range plans {
		keys = append(keys, activecopy.StrategyKey{TenantID: plan.TenantID, StrategyID: plan.StrategyID})
	}
	port.copy.Track(keys...)
	port.own.Track(keys...)
	if port.registerOwned != nil {
		port.registerOwned(qg, plans)
	}
}

// Acknowledged tells the copy, then the record what the copy says changed.
// Neither waits on I/O: the record's writes are queued.
func (port openAlertCopyPort) Acknowledged(events []contract.TriggerEventV1) {
	changed := port.copy.Acknowledged(events)
	port.own.Acknowledged(events)
	port.record.Acknowledged(events, changed)
	port.opens.Note(changed)
}

// OpenLevels is the copy's word on which Levels of the series' alert are
// open (contract.OpenLevelSet); not known without a copy.
func (port openAlertCopyPort) OpenLevels(tenantID, strategyID, fingerprint string) ([]uint32, bool) {
	return port.copy.OpenLevels(tenantID, strategyID, fingerprint)
}

// NewOpens is the strategy's opens in the hour (worker.NewOpensReporter).
func (port openAlertCopyPort) NewOpens(key newopens.Key) (newopens.Report, bool) {
	return port.opens.Report(key)
}
