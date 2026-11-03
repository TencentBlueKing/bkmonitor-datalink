package main

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openalerts"
	"github.com/go-redis/redis/v8"
)

// linkdIndex is the alert link as this process uses it: the per-strategy
// copy the workers read, and the two ports the control leader's absent
// strategy difference needs - one point read per departed strategy, and the
// reconciliation that says what its alerts are.
type linkdIndex struct {
	Cache  *openalerts.Cache
	Source *openalerts.SetSource
	// Alerts is nil when the deployment configures no reconciliation
	// endpoint. Nothing is closed then.
	Alerts openalerts.Reconciler
	// Console is the same endpoint with the rest of what it answers - the
	// roster and the alert records - or nil without one. The difference
	// against strategies that no longer exist runs only when it is set:
	// without the link's roster there is no difference to take.
	Console *openalerts.HTTPReconciler
	// Location is the source and subscriber the Cache reads through, and
	// the background discovery that can move them; see linkdLocationSwitch.
	Location *linkdLocationSwitch
}

// consoleFailureLine writes the console_call_failed line: an operation's
// failures starting, or changing class, with the class and the failures so
// far, never the address. The record and linkd_console_failures_total
// count every one.
func consoleFailureLine(logger *observability.Logger) func(op, class string, failures uint64) {
	return func(op, class string, failures uint64) {
		logger.Warn("console_call_failed", "failed", 0, 0, slog.String("op", op), slog.String("class", class),
			slog.Uint64("failures", failures))
	}
}

// effectiveTimeFailureLine writes a failed effective-time read, at most
// once a minute for each business timezone or calendar, with the failures
// since the last line.
func effectiveTimeFailureLine(logger *observability.Logger) func(dependency, class string, failures uint64) {
	return func(dependency, class string, failures uint64) {
		logger.Warn("effective_time_refresh_failed", "failed", 0, 0, slog.String("dependency", dependency),
			slog.String("class", class), slog.Uint64("failures", failures))
	}
}

func newLinkdIndex(cfg config.Config, client redis.UniversalClient, connection config.RedisConnectionConfig,
	discovery *fleet.LinkdDiscoveryFacts, open func(config.RedisConnectionConfig) (redis.UniversalClient, bool), token openalerts.TokenSource,
	now func() time.Time, logger *observability.Logger) (linkdIndex, error) {
	capacity := config.DeriveLinkdCapacity(config.DetectCapacityInputs())
	settings := cfg.PhaseTwo.Linkd
	// One read cannot monopolize the allowance for the whole worker. These
	// are scan bounds, not an assertion that an oversized SET was empty.
	limits := openalerts.ReadLimits{MaxMembers: max(1, capacity.Members/4), MaxBytes: max(1, capacity.Bytes/4), PageSize: 256,
		MaxLargeRead: config.LinkdCalibrationInterval / 4}
	// The sets are read only where the Console named them: a connection its
	// target named at startup (adopted), or one the deployment stated, which
	// the first reconciliation then confirms or not. Otherwise nothing is
	// bound and nothing is read until the Console names a place.
	var binding linkdBinding
	index := openalerts.IndexLocation{KeyPrefix: settings.Prefix(), Address: "unbound"}
	if settings.ConsoleURL != "" && settings.Connection != nil {
		var err error
		if binding, err = newLinkdBinding(client, connection, settings.Prefix(), limits); err != nil {
			return linkdIndex{}, err
		}
		index = openalerts.IndexLocation{KeyPrefix: settings.Prefix(), Address: linkdLocation(connection), Database: connection.DB}
	}
	location := &linkdLocationSwitch{current: binding, replaced: make(chan struct{}), limits: limits, open: open, cfg: cfg}
	if discovery != nil {
		location.discovery = *discovery
	}
	adopted := discovery != nil && discovery.Outcome == fleet.LinkdDiscoveryAdopted
	var reconciler openalerts.Reconciler
	var console *openalerts.HTTPReconciler
	var facts openalerts.ConsoleFacts
	var err error
	if settings.ConsoleURL != "" {
		// The link's target, and its source scope, are read from the
		// Console, which lists one target for a link alarmd reads. The
		// client has no timeout of its own: each call is bounded by its
		// caller's deadline (a calibration's derived one, a page's, the
		// absent-close round's), which the client requires (ErrNoDeadline).
		// A client-wide 5 s used to cut every one of them, longer ones
		// included.
		console, err = openalerts.NewHTTPReconciler(openalerts.HTTPReconcilerOptions{BaseURL: settings.ConsoleURL,
			Username: settings.Username, Password: settings.Password, Token: token, Client: &http.Client{}, MaxResponseBytes: int64(capacity.Bytes / 4),
			Now: now, Index: index, LocationConfirmed: adopted, OnLocationMismatch: location.relocate,
			KeyingEvery: config.LinkdCalibrationInterval, OnFailure: consoleFailureLine(logger),
			// The source this deployment's events arrive under is the one
			// subscribed to the topic they are written to.
			OutputTopic: cfg.Kafka.TriggerEvent.Topic})
		if err != nil {
			return linkdIndex{}, err
		}
		reconciler, facts = console, console
		location.console = console
	}
	// The calibration rate is not written here. ReconcileBatch is only the
	// floor, and what a round calibrates before any Console call has been
	// timed; each round then calibrates what covers every tracked strategy
	// within the calibration interval, at the pace the Console's measured p90
	// allows before the next round is due (openalerts calibrationPlan), and a
	// shortfall is reported as calibration_short. Calibration runs in its own
	// loop, each call under a deadline derived from the Console's measured
	// tail (openalerts calibrationDeadline). RefreshInterval is both rounds'
	// cadence; CycleTimeout now bounds only the index round's Redis
	// reads, left for the timeout audit.
	cache, err := openalerts.NewIndex(openalerts.IndexOptions{Source: location, Subscriber: location, Reconciler: reconciler, Now: now,
		Policy: openalerts.PolicySelfMaintain, MaxStrategies: capacity.Strategies, MaxMembers: capacity.Members, MaxBytes: capacity.Bytes,
		MaxLocalEntries: capacity.LocalEntries, ReadBatch: capacity.ReadBatch, ReconcileBatch: 1, RefreshInterval: time.Second,
		IndexInterval: time.Minute, ReconcileInterval: config.LinkdCalibrationInterval, CalibrationMaxAge: 2 * config.LinkdCalibrationInterval,
		LocalRetention: time.Minute, CycleTimeout: 5 * time.Second, Facts: facts})
	if err != nil {
		return linkdIndex{}, err
	}
	location.mu.Lock()
	location.calibrate = cache.LocationMoved
	location.mu.Unlock()
	return linkdIndex{Cache: cache, Source: binding.source, Alerts: reconciler, Console: console, Location: location}, nil
}
