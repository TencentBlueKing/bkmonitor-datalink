package config

import "testing"

func TestQueryCooldownOptIn(t *testing.T) {
	cfg := PhaseTwoSchedulerConfig{}
	if cfg.RecoveryLimits().QueryUnavailableCooldown {
		t.Fatal("cooldown must be opt-in")
	}
	cfg.QueryUnavailableCooldown = true
	if !cfg.RecoveryLimits().QueryUnavailableCooldown {
		t.Fatal("cooldown setting was not passed to scheduler")
	}
}

func TestQueryCooldownStrictYAML(t *testing.T) {
	for _, value := range []string{"true", "false"} {
		text := validGoAccessRuntimeConfigYAML("cooldown-worker") + "  scheduler:\n    query_unavailable_cooldown: " + value + "\n"
		cfg, err := Load(writeConfig(t, text))
		if err != nil || cfg.PhaseTwo.Scheduler.QueryUnavailableCooldown != (value == "true") {
			t.Fatalf("value=%s cfg=%+v error=%v", value, cfg.PhaseTwo.Scheduler, err)
		}
	}
}
