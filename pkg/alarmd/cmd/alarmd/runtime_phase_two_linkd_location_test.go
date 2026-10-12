package main

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openalerts"
)

// linkdLocationConfig is a deployment whose runtime Redis is a sentinel and
// whose platform's own Redis - held for the dynamic groups - is a standalone
// instance, the shape where the link's hook writes to the latter.
func linkdLocationConfig() config.Config {
	var cfg config.Config
	cfg.Redis.Mode = config.RedisModeSentinel
	cfg.Redis.MasterName = "runtime-master"
	cfg.Redis.SentinelAddress = []string{"sentinel:26379"}
	cfg.Redis.Password = "runtime-secret"
	cfg.Redis.DB = 8
	prefix := "platform"
	cfg.PlatformCache.DynamicGroupKeyPrefix = &prefix
	cfg.PlatformCache.TargetGroup = &config.RedisConnectionConfig{Mode: config.RedisModeStandalone, Address: "platform-redis:6379", Password: "platform-secret"}
	cfg.PhaseTwo.Linkd.ConsoleURL = "http://console/base"
	cfg.PhaseTwo.Linkd.Username, cfg.PhaseTwo.Linkd.Password = "user", "secret"
	return cfg
}

func answering(target openalerts.TargetBinding, calls *int) discoverLinkdTarget {
	return func(_ context.Context, options openalerts.HTTPReconcilerOptions) (openalerts.TargetBinding, error) {
		*calls++
		if options.BaseURL != "http://console/base" || options.Username != "user" || options.Password != "secret" {
			return openalerts.TargetBinding{}, errors.New("unexpected options")
		}
		return target, nil
	}
}

func TestTheSetsAreReadWhereTheLinkWritesThemWithCredentialsAlreadyHeld(t *testing.T) {
	calls := 0
	cfg := adoptedConfig(adoptLinkdLocation(context.Background(), linkdLocationConfig(), answering(openalerts.TargetBinding{
		KeyPrefix: "hook:open", Address: "PLATFORM-redis:6379", Database: 8}, &calls)))
	got := cfg.PhaseTwo.Linkd.Connection
	if got == nil || got.Mode != config.RedisModeStandalone || got.Address != "platform-redis:6379" || got.Password != "platform-secret" || got.DB != 8 {
		t.Fatalf("connection %+v", got)
	}
	if cfg.PhaseTwo.Linkd.Prefix() != "hook:open" || calls != 1 {
		t.Fatalf("prefix %q calls %d", cfg.PhaseTwo.Linkd.Prefix(), calls)
	}
	if cfg.PlatformCache.TargetGroup.DB != 0 {
		t.Fatal("the held connection itself was moved to the link's database")
	}
}

func TestASentinelTargetIsMatchedTheWayTheConsoleNamesIt(t *testing.T) {
	calls := 0
	cfg := adoptedConfig(adoptLinkdLocation(context.Background(), linkdLocationConfig(), answering(openalerts.TargetBinding{
		KeyPrefix: "alarmd:open_alerts", Address: "sentinel:runtime-master (sentinel:26379)", Database: 5}, &calls)))
	got := cfg.PhaseTwo.Linkd.Connection
	if got == nil || got.MasterName != "runtime-master" || got.Password != "runtime-secret" || got.DB != 5 {
		t.Fatalf("connection %+v", got)
	}
}

// What is not adopted: a Redis this process holds no connection to, and a
// Console that never answers; without a Console nothing is asked. The
// Console is always asked when there is one, and its prefix is the one read:
// the deployment no longer states either.
func TestTheLinksLocationIsAdoptedOnlyWhenNothingElseDecidesIt(t *testing.T) {
	calls := 0
	noConsole := linkdLocationConfig()
	noConsole.PhaseTwo.Linkd.ConsoleURL = ""
	if got := adoptedConfig(adoptLinkdLocation(context.Background(), noConsole, answering(openalerts.TargetBinding{Address: "platform-redis:6379"}, &calls))); got.PhaseTwo.Linkd.Connection != nil || calls != 0 {
		t.Fatal("adopted without a Console")
	}
	if got := adoptedConfig(adoptLinkdLocation(context.Background(), linkdLocationConfig(), answering(openalerts.TargetBinding{KeyPrefix: "hook:open", Address: "platform-redis:6379", Database: 8}, &calls))); got.PhaseTwo.Linkd.Prefix() != "hook:open" || calls != 1 {
		t.Fatalf("prefix %q after %d asks, want the Console's prefix from one ask", got.PhaseTwo.Linkd.Prefix(), calls)
	}
	if got := adoptedConfig(adoptLinkdLocation(context.Background(), linkdLocationConfig(), answering(openalerts.TargetBinding{Address: "elsewhere:6379", Database: 8}, &calls))); got.PhaseTwo.Linkd.Connection != nil {
		t.Fatalf("a Redis this process holds no credentials for was adopted: %+v", got.PhaseTwo.Linkd.Connection)
	}
	defer func(pause time.Duration) { linkdDiscoveryPause = pause }(linkdDiscoveryPause)
	linkdDiscoveryPause = 0
	failing := 0
	silent := func(context.Context, openalerts.HTTPReconcilerOptions) (openalerts.TargetBinding, error) {
		failing++
		return openalerts.TargetBinding{}, errors.New("console down")
	}
	if got := adoptedConfig(adoptLinkdLocation(context.Background(), linkdLocationConfig(), silent)); got.PhaseTwo.Linkd.Connection != nil || failing != linkdDiscoveryAttempts {
		t.Fatalf("attempts %d", failing)
	}
}

// A Console that answers on a later attempt is still adopted.
func TestAConsoleThatAnswersOnRetryIsAdopted(t *testing.T) {
	defer func(pause time.Duration) { linkdDiscoveryPause = pause }(linkdDiscoveryPause)
	linkdDiscoveryPause = 0
	attempts := 0
	late := func(context.Context, openalerts.HTTPReconcilerOptions) (openalerts.TargetBinding, error) {
		attempts++
		if attempts < linkdDiscoveryAttempts {
			return openalerts.TargetBinding{}, errors.New("not yet")
		}
		return openalerts.TargetBinding{KeyPrefix: "alarmd:open_alerts", Address: "platform-redis:6379", Database: 8}, nil
	}
	if got := adoptedConfig(adoptLinkdLocation(context.Background(), linkdLocationConfig(), late)); got.PhaseTwo.Linkd.Connection == nil || got.PhaseTwo.Linkd.Connection.DB != 8 {
		t.Fatal("a late answer was not adopted")
	}
}

// adoptedConfig is the configuration adoptLinkdLocation returns, for the
// tests that read only where the sets are read from.
func adoptedConfig(cfg config.Config, _ *fleet.LinkdDiscoveryFacts) config.Config { return cfg }

// Each way the startup question can end is recorded with what the page needs
// to say it: before this, a Console that refused the credentials left the
// configuration as it was and nothing anywhere said so.
func TestEveryDiscoveryOutcomeIsRecordedWithWhatItFound(t *testing.T) {
	defer func(pause time.Duration) { linkdDiscoveryPause = pause }(linkdDiscoveryPause)
	linkdDiscoveryPause = 0
	calls := 0
	here := openalerts.TargetBinding{EventSourceID: "source", HookName: "active", KeyPrefix: "hook:open", Address: "platform-redis:6379", Database: 8}
	elsewhere := here
	elsewhere.Address = "elsewhere:6379"

	if _, facts := adoptLinkdLocation(context.Background(), linkdLocationConfig(), answering(here, &calls)); facts == nil ||
		facts.Outcome != fleet.LinkdDiscoveryAdopted || facts.Attempts != 1 || facts.Target == nil ||
		*facts.Target != (fleet.LinkdTargetFacts{EventSourceID: "source", HookName: "active", Address: "platform-redis:6379", Database: 8, KeyPrefix: "hook:open"}) {
		t.Fatalf("adopted: %+v", facts)
	}
	if _, facts := adoptLinkdLocation(context.Background(), linkdLocationConfig(), answering(elsewhere, &calls)); facts == nil ||
		facts.Outcome != fleet.LinkdDiscoveryNoHeldConnection || facts.Target == nil || facts.Target.Address != "elsewhere:6379" {
		t.Fatalf("no held connection: %+v", facts)
	}
	refused := func(context.Context, openalerts.HTTPReconcilerOptions) (openalerts.TargetBinding, error) {
		return openalerts.TargetBinding{}, errors.New("alarmd openalerts: Console HTTP status 401")
	}
	if _, facts := adoptLinkdLocation(context.Background(), linkdLocationConfig(), refused); facts == nil ||
		facts.Outcome != fleet.LinkdDiscoveryFailed || facts.Attempts != linkdDiscoveryAttempts ||
		facts.Error != "alarmd openalerts: Console HTTP status 401" || facts.Target != nil {
		t.Fatalf("refused: %+v", facts)
	}
	long := func(context.Context, openalerts.HTTPReconcilerOptions) (openalerts.TargetBinding, error) {
		return openalerts.TargetBinding{}, errors.New(strings.Repeat("x", 4*linkdFailureTextLimit))
	}
	if _, facts := adoptLinkdLocation(context.Background(), linkdLocationConfig(), long); len(facts.Error) != linkdFailureTextLimit+3 {
		t.Fatalf("a failure text is carried whole: %d bytes", len(facts.Error))
	}
	if !reflect.DeepEqual(fleet.LinkdDiscoveryOutcomes, []string{fleet.LinkdDiscoveryAdopted, fleet.LinkdDiscoveryNoHeldConnection, fleet.LinkdDiscoveryFailed}) {
		t.Fatalf("discovery outcomes = %v, want adopted, no held connection and failed: the deployment states no connection", fleet.LinkdDiscoveryOutcomes)
	}
	noConsole := linkdLocationConfig()
	noConsole.PhaseTwo.Linkd.ConsoleURL = ""
	if _, facts := adoptLinkdLocation(context.Background(), noConsole, answering(here, &calls)); facts != nil {
		t.Fatalf("a deployment without a Console recorded %+v", facts)
	}
}

// What the Console listed is carried with the discovery, so a link listing
// several targets is read in one step: one on adoption, with its name; on a
// refusal, the count and the names.
func TestTheDiscoveryCarriesWhatTheConsoleListed(t *testing.T) {
	calls := 0
	_, facts := adoptLinkdLocation(context.Background(), linkdLocationConfig(), answering(openalerts.TargetBinding{
		EventSourceID: "alarmd", HookName: "active", KeyPrefix: "hook:open", Address: "platform-redis:6379", Database: 8}, &calls))
	if facts == nil || facts.TargetCount != 1 || !reflect.DeepEqual(facts.TargetNames, []string{"alarmd/active"}) {
		t.Fatalf("adopted: %+v, want one target named alarmd/active", facts)
	}
	defer func(pause time.Duration) { linkdDiscoveryPause = pause }(linkdDiscoveryPause)
	linkdDiscoveryPause = 0
	several := func(context.Context, openalerts.HTTPReconcilerOptions) (openalerts.TargetBinding, error) {
		return openalerts.TargetBinding{}, &openalerts.TargetCountError{Count: 2, Names: []string{"alarmd/active", "other/standby"}}
	}
	_, facts = adoptLinkdLocation(context.Background(), linkdLocationConfig(), several)
	if facts == nil || facts.Outcome != fleet.LinkdDiscoveryFailed || facts.TargetCount != 2 ||
		!reflect.DeepEqual(facts.TargetNames, []string{"alarmd/active", "other/standby"}) {
		t.Fatalf("refused: %+v, want the count and both names", facts)
	}
	many := make([]string, 20)
	for index := range many {
		many[index] = fmt.Sprintf("source-%d/hook", index)
	}
	crowded := func(context.Context, openalerts.HTTPReconcilerOptions) (openalerts.TargetBinding, error) {
		return openalerts.TargetBinding{}, &openalerts.TargetCountError{Count: len(many), Names: many}
	}
	if _, facts = adoptLinkdLocation(context.Background(), linkdLocationConfig(), crowded); facts.TargetCount != 20 ||
		!reflect.DeepEqual(facts.TargetNames, many[:fleet.MaxLinkdTargetNames]) {
		t.Fatalf("twenty targets: count %d, names %v; want the count whole and the first %d names", facts.TargetCount, facts.TargetNames, fleet.MaxLinkdTargetNames)
	}
}
