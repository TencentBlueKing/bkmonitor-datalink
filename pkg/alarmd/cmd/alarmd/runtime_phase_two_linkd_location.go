package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openalerts"
)

// discoverLinkdTarget asks the link which of its targets is this deployment's.
type discoverLinkdTarget func(context.Context, openalerts.HTTPReconcilerOptions) (openalerts.TargetBinding, error)

// linkdDiscoveryAttempts bounds how long startup waits on a Console that does
// not answer. Detection does not depend on the link; a process that could
// not ask reads no set meanwhile - its gate answers from what it sent - and
// keeps asking in the background (linkdLocationSwitch.retry) until the
// Console answers, then reads where the link writes without a restart.
const linkdDiscoveryAttempts = 3

var linkdDiscoveryPause = 2 * time.Second

// adoptLinkdLocation reads the open alert sets from where the link writes
// them. A deployment states the Console and nothing else: the link's target
// names the Redis and database its hook writes to, and when one of the Redis
// connections this process already holds is that Redis, its credentials are
// used with the link's database and prefix. The deployment therefore never
// restates the hook's Redis a second time. A target at a Redis this process
// holds no connection to is left alone: the reconciler then refuses with both
// locations named.
//
// What happened is returned beside the configuration, because the answer is
// otherwise lost: a Console refusing the credentials, or a link writing to a
// Redis this process holds no connection to, leaves the configuration as it
// was and every later round refusing, with nothing that says why.
func adoptLinkdLocation(ctx context.Context, cfg config.Config, discover discoverLinkdTarget) (config.Config, *fleet.LinkdDiscoveryFacts) {
	settings := cfg.PhaseTwo.Linkd
	if settings.ConsoleURL == "" {
		return cfg, nil
	}
	options := openalerts.HTTPReconcilerOptions{BaseURL: settings.ConsoleURL, Username: settings.Username, Password: settings.Password,
		Client: &http.Client{Timeout: 5 * time.Second}, MaxResponseBytes: 1 << 20}
	var target openalerts.TargetBinding
	var err error
	facts := &fleet.LinkdDiscoveryFacts{}
	for attempt := 0; attempt < linkdDiscoveryAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				facts.Outcome, facts.Error = fleet.LinkdDiscoveryFailed, boundedText(ctx.Err().Error())
				return cfg, facts
			case <-time.After(linkdDiscoveryPause):
			}
		}
		facts.Attempts++
		if target, err = discover(ctx, options); err == nil {
			break
		}
	}
	if err != nil {
		facts.Outcome, facts.Error = fleet.LinkdDiscoveryFailed, boundedText(err.Error())
		noteListedTargets(facts, target, err)
		return cfg, facts
	}
	facts.Target = linkdTargetFacts(target)
	noteListedTargets(facts, target, nil)
	connection, prefix, found := heldLinkdLocation(cfg, target)
	if !found {
		facts.Outcome = fleet.LinkdDiscoveryNoHeldConnection
		return cfg, facts
	}
	cfg.PhaseTwo.Linkd.Connection = &connection
	cfg.PhaseTwo.Linkd.KeyPrefix = prefix
	facts.Outcome = fleet.LinkdDiscoveryAdopted
	return cfg, facts
}

// heldLinkdLocation is where this process reads the sets a target writes:
// one of the Redis connections it already holds, pointed at the target's
// database, under the stated prefix or else the target's. Not found when no
// held connection is at the target's Redis.
func heldLinkdLocation(cfg config.Config, target openalerts.TargetBinding) (config.RedisConnectionConfig, string, bool) {
	for _, held := range heldRedisConnections(cfg) {
		if !strings.EqualFold(linkdLocation(held), target.Address) {
			continue
		}
		held.DB = target.Database
		return held, target.KeyPrefix, true
	}
	return config.RedisConnectionConfig{}, "", false
}

// noteListedTargets records what the Console listed when it was asked: the
// one target it was answered with, or, when it was refused for listing other
// than one, how many and their names. A refusal for any other reason - the
// Console not answering - leaves the last listing as it was.
func noteListedTargets(facts *fleet.LinkdDiscoveryFacts, target openalerts.TargetBinding, err error) {
	if err == nil {
		facts.TargetCount, facts.TargetNames = 1, linkdTargetNames([]string{target.EventSourceID + "/" + target.HookName})
		return
	}
	var counted *openalerts.TargetCountError
	if errors.As(err, &counted) {
		facts.TargetCount, facts.TargetNames = counted.Count, linkdTargetNames(counted.Names)
	}
}

// linkdTargetNames is the names a discovery carries: the first
// MaxLinkdTargetNames, each bounded as a failure sentence is.
func linkdTargetNames(names []string) []string {
	kept := make([]string, 0, min(len(names), fleet.MaxLinkdTargetNames))
	for _, name := range names[:min(len(names), fleet.MaxLinkdTargetNames)] {
		kept = append(kept, boundedText(name))
	}
	return kept
}

// linkdTargetFacts is a target as the page shows it: where the link writes,
// never its credentials, which the Console does not publish.
func linkdTargetFacts(target openalerts.TargetBinding) *fleet.LinkdTargetFacts {
	return &fleet.LinkdTargetFacts{EventSourceID: target.EventSourceID, HookName: target.HookName,
		Address: target.Address, Database: target.Database, KeyPrefix: target.KeyPrefix}
}

// linkdFailureTextLimit bounds a failure sentence carried on the snapshot.
// The Console's own errors list every target it maintains, which on a link
// with many hooks is long.
const linkdFailureTextLimit = 512

func boundedText(text string) string {
	if len(text) > linkdFailureTextLimit {
		return text[:linkdFailureTextLimit] + "..."
	}
	return text
}

// heldRedisConnections are the Redis connections the deployment already gave
// this process, runtime Redis first.
func heldRedisConnections(cfg config.Config) []config.RedisConnectionConfig {
	held := []config.RedisConnectionConfig{cfg.RuntimeStoreRedis()}
	if group, ok := cfg.TargetGroupRedis(); ok {
		held = append(held, group)
	}
	if dynamic, ok := cfg.DynamicConfigRedis(); ok {
		held = append(held, dynamic)
	}
	held = append(held, cfg.CMDBCacheRedis())
	if strategy := cfg.PlatformCache.Strategy; strategy != nil {
		held = append(held, *strategy)
	}
	if service := cfg.Kafka.LegacyAdapter.ServiceRedis; service.Mode != "" {
		held = append(held, service)
	}
	return held
}

// linkdLocation is a connection written the way the link's Console names the
// Redis a target writes to.
func linkdLocation(connection config.RedisConnectionConfig) string {
	if connection.Mode == config.RedisModeSentinel {
		return "sentinel:" + connection.MasterName + " (" + strings.Join(connection.SentinelAddress, ", ") + ")"
	}
	return connection.Address
}
