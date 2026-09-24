package config

import "testing"

func TestQueryCooldownDefaultEnabled(t *testing.T) {
	cfg := Default().PhaseTwo.Scheduler
	if !cfg.RecoveryLimits().QueryUnavailableCooldown {
		t.Fatal("product default must enable cooldown")
	}
	cfg.QueryUnavailableCooldown = false
	if cfg.RecoveryLimits().QueryUnavailableCooldown {
		t.Fatal("explicit disable was not passed to scheduler")
	}
}

func TestQueryCooldownStrictYAML(t *testing.T) {
	for _, value := range []string{"", "true", "false"} {
		text := validGoAccessRuntimeConfigYAML("cooldown-worker")
		if value != "" {
			text += "  scheduler:\n    query_unavailable_cooldown: " + value + "\n"
		}
		cfg, err := Load(writeConfig(t, text))
		if err != nil || cfg.PhaseTwo.Scheduler.QueryUnavailableCooldown != (value != "false") {
			t.Fatalf("value=%s cfg=%+v error=%v", value, cfg.PhaseTwo.Scheduler, err)
		}
	}
}
