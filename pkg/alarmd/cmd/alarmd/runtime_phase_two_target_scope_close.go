package main

import (
	"context"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/access"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/activecopy"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scopeclose"
)

// targetScopeCloseInterval is how often the close decides. The decision
// needs two Slots of the same strategy, and the shortest evaluation interval
// is a minute; twice a minute keeps a confirmed observation from waiting a
// whole Slot for its close without making the step itself a load.
const targetScopeCloseInterval = 30 * time.Second

// targetScopeCloseLoop runs the superseded-identity close's decisions on
// this replica (scopeclose; the family keeps the target-scope name). The
// offers come from this replica's own admission step, so every replica runs
// its own loop over what it saw; there is no leader and nothing shared.
type targetScopeCloseLoop struct {
	bundle *phaseTwoWorkerBundle
	closer *scopeclose.Closer
}

func (loop targetScopeCloseLoop) run(ctx context.Context) {
	ticker := time.NewTicker(targetScopeCloseInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		loop.bundle.mu.RLock()
		stopping := loop.bundle.draining || loop.bundle.closed
		loop.bundle.mu.RUnlock()
		if stopping {
			continue
		}
		step, cancel := context.WithTimeout(ctx, targetScopeCloseInterval/2)
		loop.closer.Step(step)
		cancel()
	}
}

// targetScopeCloseFor builds the superseded-identity close and the sink the
// admission step offers to, only for a deployment with the link's control
// plane. Without it there is no copy to judge against and no filter to
// close through, so the close could never act. The sink is then a nil
// interface, which the access path reads as "nobody listens".
func targetScopeCloseFor(cfg config.Config, now func() time.Time) (*scopeclose.Closer, access.SupersededSink) {
	if !cfg.PhaseTwo.Linkd.ControlPlane.Configured() {
		return nil, nil
	}
	budget := config.DeriveLinkdCapacity(config.DetectCapacityInputs())
	closer := scopeclose.New(scopeclose.Options{Now: now,
		MaxEntries: budget.LocalEntries, Batch: budget.CloseBatch})
	return closer, supersededSink{closer: closer}
}

// bindTargetScopeClose binds the close to the copy and the link's filter,
// its counts to the metric and its loop to the bundle. Nothing is wired for
// a nil close.
func bindTargetScopeClose(bundle *phaseTwoWorkerBundle, closer *scopeclose.Closer, copy *activecopy.Copy, route *filterRoute,
	recorder *metric.Recorder) {
	if closer == nil {
		return
	}
	var set scopeclose.OpenSet
	if copy != nil {
		set = copy
	}
	if route != nil {
		closer.Bind(set, route.closer, route.source)
	} else {
		closer.Bind(set, nil, "")
	}
	recorder.SetTargetScopeCloseSource(closer.Stats)
	bundle.targetScopeClose = closer
	bundle.dependencies.RunTargetScopeClose = targetScopeCloseLoop{bundle: bundle, closer: closer}.run
}

// supersededSink hands the admission step's offers to the close: the
// per-query screen and the offers of the strategies it cleared.
type supersededSink struct{ closer *scopeclose.Closer }

func (sink supersededSink) Screen(plan execution.PlanIdentity) bool {
	return sink.closer.Offers(scopeclose.Key{TenantID: plan.TenantID, StrategyID: plan.StrategyID})
}

func (sink supersededSink) Observe(offer access.SupersededOffer) {
	sink.closer.Observe(scopeclose.Offer{TenantID: offer.Plan.TenantID, StrategyID: offer.Plan.StrategyID,
		Fingerprint: offer.Fingerprint, StrategyRevision: offer.StrategyRevision, Round: offer.Round,
		IntervalSeconds: offer.IntervalSeconds, ReadOffset: offer.ReadOffset})
}

// withTargetScopeClose puts the close's reading beside the copy it judges
// from, in the replica's facts and so in fleet.get and the health view.
func withTargetScopeClose(source func() *fleet.ActiveCopyFacts, closer *scopeclose.Closer) func() *fleet.ActiveCopyFacts {
	return func() *fleet.ActiveCopyFacts {
		facts := source()
		if facts != nil && closer != nil {
			facts.TargetScopeClose = targetScopeCloseFacts(closer.Facts())
		}
		return facts
	}
}

// targetScopeCloseFacts carries the close's facts field for field.
func targetScopeCloseFacts(facts scopeclose.Facts) *fleet.TargetScopeCloseFacts {
	result := &fleet.TargetScopeCloseFacts{Pending: facts.Pending, Confirmed: facts.Confirmed,
		MaxEntries: facts.MaxEntries, Outcomes: facts.Outcomes}
	for _, row := range facts.Strategies {
		result.Strategies = append(result.Strategies, fleet.TargetScopeCloseStrategy{TenantID: row.TenantID,
			StrategyID: row.StrategyID, Pending: row.Pending, Confirmed: row.Confirmed, Outcomes: row.Outcomes,
			PendingSample: row.PendingSample, DecidedSample: row.DecidedSample})
	}
	return result
}
