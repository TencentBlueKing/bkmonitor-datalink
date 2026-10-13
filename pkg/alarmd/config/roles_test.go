// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package config

import (
	"reflect"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/roles"
)

func TestLegacyConfigSelectsDefaultSharedRoles(t *testing.T) {
	for _, cfg := range []Config{Default(), validGoAccessConfigObject()} {
		if got := cfg.EffectiveRoles(); !reflect.DeepEqual(got, roles.Set{roles.Control, roles.Worker, roles.Channel}) {
			t.Fatalf("legacy EffectiveRoles() = %v", got)
		}
	}
	cfg, err := LoadWithRoles(writeConfig(t, validGoAccessRuntimeConfigYAML("legacy-instance")), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.Roles, roles.Set{roles.Control, roles.Worker, roles.Channel}) {
		t.Fatalf("LoadWithRoles(, nil) default roles = %v", cfg.Roles)
	}
	copy := cfg.EffectiveRoles()
	copy[0] = roles.Channel
	if !cfg.HasRole(roles.Control) {
		t.Fatal("mutating returned roles changed the configuration")
	}
}

func TestLoadRoleSelectionsAndCLIOverrideBeforeDependencyChecks(t *testing.T) {
	channelYAML := `roles: [channel]
redis:
  address: runtime.test:6379
phase_two:
  worker:
    id: channel-instance
`
	for _, selected := range []roles.Set{
		{roles.Control}, {roles.Worker}, {roles.Channel}, {roles.Control, roles.Channel},
		{roles.Worker, roles.Channel}, {roles.Control, roles.Worker},
		{roles.Channel, roles.Worker, roles.Control},
	} {
		t.Run(selected.String(), func(t *testing.T) {
			contents := "roles: [" + selected.String() + "]\n" + validGoAccessRuntimeConfigYAML("instance")
			cfg, err := LoadWithRoles(writeConfig(t, contents), nil)
			if err != nil || !reflect.DeepEqual(cfg.Roles, selected) {
				t.Fatalf("LoadWithRoles(, nil) = roles %v, error %v; want %v", cfg.Roles, err, selected)
			}
		})
	}
	cfg, err := LoadWithRoles(writeConfig(t, channelYAML), nil)
	if err != nil || !reflect.DeepEqual(cfg.Roles, roles.Set{roles.Channel}) {
		t.Fatalf("nil override did not keep channel: %v, %v", cfg.Roles, err)
	}
	for _, fileSelection := range []string{"[control]", "[]", "null", "[unknown]", "[worker,worker]"} {
		contents := strings.Replace(channelYAML, "[channel]", fileSelection, 1)
		cfg, err := LoadWithRoles(writeConfig(t, contents), roles.Set{roles.Channel})
		if err != nil || !reflect.DeepEqual(cfg.Roles, roles.Set{roles.Channel}) {
			t.Fatalf("channel override of %s = %v, %v", fileSelection, cfg.Roles, err)
		}
	}
	if _, err := LoadWithRoles(writeConfig(t, channelYAML), roles.Set{roles.Worker}); err == nil || !strings.Contains(err.Error(), "broker") {
		t.Fatalf("Worker override did not validate its dependencies: %v", err)
	}
	if _, err := LoadWithRoles(writeConfig(t, channelYAML), roles.Set{}); err == nil || !strings.Contains(err.Error(), "roles") {
		t.Fatalf("empty CLI override accepted: %v", err)
	}
	if _, err := LoadWithRoles("", roles.Set{}); err == nil || !strings.Contains(err.Error(), "roles") {
		t.Fatalf("empty override accepted without file: %v", err)
	}
	if _, err := LoadWithRoles("", roles.Set{roles.Channel}); err == nil || !strings.Contains(err.Error(), "redis") {
		t.Fatalf("channel override without file did not check Redis: %v", err)
	}
}

func TestLoadRejectsExplicitInvalidRoles(t *testing.T) {
	for _, selection := range []string{"[]", "null", "", "[unknown]", "[worker,worker]", "[' worker']", "['']"} {
		contents := "roles: " + selection + "\n" + validGoAccessRuntimeConfigYAML("instance")
		if _, err := LoadWithRoles(writeConfig(t, contents), nil); err == nil || !strings.Contains(err.Error(), "roles") {
			t.Fatalf("roles: %s error = %v, want named rejection", selection, err)
		}
	}
	// An explicit null supplied by a legal YAML merge also remains explicit.
	merged := "<<: &defaults {roles: null}\n" + validGoAccessRuntimeConfigYAML("instance")
	if _, err := LoadWithRoles(writeConfig(t, merged), nil); err == nil || !strings.Contains(err.Error(), "roles") {
		t.Fatalf("merged null roles error = %v", err)
	}
}

func TestChannelOnlyRequiresPublicRuntimeDependencies(t *testing.T) {
	cfg := Default()
	cfg.Roles = roles.Set{roles.Channel}
	cfg.Redis.Address = "runtime.test:6379"
	cfg.PhaseTwo.Worker.ID = "channel-instance"
	// These dependencies are absent or invalid on purpose: the channel does
	// not initialize a compiler, a query runner or a business output.
	cfg.PlatformCache.Strategy = &RedisConnectionConfig{}
	cfg.PlatformCache.CMDB = &RedisConnectionConfig{}
	cfg.PlatformCache.TargetGroup = &RedisConnectionConfig{}
	cfg.PhaseTwo.Linkd.ConsoleURL = "invalid"
	cfg.PhaseTwo.Scheduler = PhaseTwoSchedulerConfig{}
	cfg.Limits = LimitsConfig{}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("channel required business dependencies: %v", err)
	}
	for name, mutate := range map[string]func(*Config){
		"http listen":      func(c *Config) { c.HTTP.Listen = "bad" },
		"redis":            func(c *Config) { c.Redis.Address = "" },
		"state_prefix":     func(c *Config) { c.Redis.StatePrefix = "" },
		"state TTL":        func(c *Config) { c.Redis.MaxTTL = c.Redis.MinTTL - 1 },
		"worker identity":  func(c *Config) { c.PhaseTwo.Worker.ID = "" },
		"registration":     func(c *Config) { c.PhaseTwo.Worker.RegistrationRenewInterval = c.PhaseTwo.Worker.RegistrationTTL },
		"shutdown_timeout": func(c *Config) { c.ShutdownTimeout = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			changed := cfg
			mutate(&changed)
			if err := changed.Validate(); err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("%s error = %v", name, err)
			}
		})
	}
}

func TestRoleSpecificBusinessDependenciesAndLeaseBudgets(t *testing.T) {
	control := validGoAccessConfigObject()
	control.Roles = roles.Set{roles.Control}
	control.PhaseTwo.Access.UQEndpoint = ""
	control.PlatformCache.CMDB = &RedisConnectionConfig{}
	if err := control.Validate(); err != nil {
		t.Fatalf("Control required Worker-only dependencies: %v", err)
	}
	worker := validGoAccessConfigObject()
	worker.Roles = roles.Set{roles.Worker}
	worker.PhaseTwo.Control.StrategyCachePrefix = ""
	worker.PhaseTwo.Control.ProviderRoute = ""
	worker.PhaseTwo.Control.Timezone = ""
	if err := worker.Validate(); err != nil {
		t.Fatalf("Worker required Control-only source coordinates: %v", err)
	}
	for _, test := range []struct {
		name    string
		cfg     Config
		mutate  func(*Config)
		message string
	}{
		{"Control strategy prefix", control, func(c *Config) { c.PhaseTwo.Control.StrategyCachePrefix = "" }, "control source"},
		{"Control strategy Redis", control, func(c *Config) { c.PlatformCache.Strategy = &RedisConnectionConfig{} }, "platform_cache.strategy"},
		{"Control compiler", control, func(c *Config) { c.Limits.Compiler.MaxPlanBytes = 0 }, "limits.compiler"},
		{"Control global-close Kafka", control, func(c *Config) { c.Kafka.Brokers = nil }, "broker"},
		{"Control global-close Linkd", control, func(c *Config) { c.PhaseTwo.Linkd.ConsoleURL = "invalid" }, "linkd"},
		{"Control leader lease", control, func(c *Config) {
			c.PhaseTwo.Ownership.ControlLeaderRenewInterval = c.PhaseTwo.Ownership.ControlLeaderTTL
		}, "ownership TTL"},
		{"Control completion reserve", control, func(c *Config) { c.PhaseTwo.Access.DownstreamExecutionReserve = 0 }, "downstream_execution_reserve"},
		{"Worker UQ", worker, func(c *Config) { c.PhaseTwo.Access.UQEndpoint = "" }, "access UQ"},
		{"Worker CMDB", worker, func(c *Config) { c.PlatformCache.CMDB = &RedisConnectionConfig{} }, "platform_cache.cmdb"},
		{"Worker effective-time Redis", worker, func(c *Config) { c.PlatformCache.Strategy = &RedisConnectionConfig{} }, "platform_cache.strategy"},
		{"Worker QG lease", worker, func(c *Config) { c.PhaseTwo.Ownership.LeaseRenewInterval = c.PhaseTwo.Ownership.LeaseTTL }, "ownership TTL"},
		{"Worker mutation budget", worker, func(c *Config) { c.PhaseTwo.Coordinator.MaxStateMutations = c.chunkedStateApplyBudget() + 1 }, "chunked state"},
		{"Worker output evidence", worker, func(c *Config) { c.Kafka.TriggerEvent.MaxMessageBytes = c.Limits.Trigger.MaxEvidenceBytesPerEvent - 1 }, "maximum trigger evidence"},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.mutate(&test.cfg)
			if err := test.cfg.Validate(); err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("Validate() = %v; want %q", err, test.message)
			}
		})
	}
}

func TestUnusedRoleCredentialsDoNotBlockLoad(t *testing.T) {
	t.Setenv(LinkdConsoleUsernameEnvironment, "")
	t.Setenv(CLIAdminKeyEnvironment, "from-environment")
	contents := validGoAccessRuntimeConfigYAML("instance") + `cli:
  admin_key: from-file
`
	if _, err := LoadWithRoles(writeConfig(t, contents), roles.Set{roles.Worker}); err != nil {
		t.Fatalf("unused CLI credentials blocked Worker: %v", err)
	}
	channel := `roles: [channel]
redis:
  address: runtime.test:6379
phase_two:
  worker:
    id: channel
  linkd:
    username: from-file
`
	t.Setenv(CLIAdminKeyEnvironment, "")
	t.Setenv(LinkdConsoleUsernameEnvironment, "from-environment")
	if _, err := LoadWithRoles(writeConfig(t, channel), nil); err != nil {
		t.Fatalf("unused Linkd credentials blocked channel: %v", err)
	}
}

func TestChannelAuthenticationAndPublicSurface(t *testing.T) {
	cfg := Default()
	cfg.Roles = roles.Set{roles.Channel}
	cfg.Redis.Address = "runtime.test:6379"
	cfg.PhaseTwo.Worker.ID = "channel"
	cfg.CLI = CLIConfig{EnvironmentID: "test", EnvironmentName: "Test",
		PublicBaseURL: "https://alarmd.test/cli", AdminKey: strings.Repeat("k", 40)}
	if err := cfg.Validate(); err != nil || !cfg.PublicSurfaceRestrictionRequested() {
		t.Fatalf("valid channel auth error = %v", err)
	}
	for _, mutate := range []func(*CLIConfig){
		func(c *CLIConfig) { c.EnvironmentID = "" },
		func(c *CLIConfig) { c.EnvironmentName = "bad\nname" },
		func(c *CLIConfig) { c.PublicBaseURL = "https://alarmd.test/?token=secret" },
		func(c *CLIConfig) { c.PublicBaseURL = "https://alarmd.test/../cli" },
		func(c *CLIConfig) { c.AdminKey = "short" },
		func(c *CLIConfig) { c.AdminKey = strings.Repeat(" ", 40) },
	} {
		changed := cfg
		mutate(&changed.CLI)
		if err := changed.Validate(); err == nil || !strings.Contains(err.Error(), "cli ") {
			t.Fatalf("invalid enabled channel auth error = %v", err)
		}
	}
	cfg.CLI.AdminKey = ""
	if err := cfg.Validate(); err != nil || cfg.PublicSurfaceRestrictionRequested() {
		t.Fatalf("disabled legacy auth error = %v", err)
	}
	cfg.CLI.AdminKey = strings.Repeat("k", 40)
	cfg.Roles = roles.Set{roles.Worker}
	if cfg.PublicSurfaceRestrictionRequested() {
		t.Fatal("Worker-only public surface was restricted by an unused CLI key")
	}
}
