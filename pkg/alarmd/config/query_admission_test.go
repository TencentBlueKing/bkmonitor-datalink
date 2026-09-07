package config

import "testing"

func TestExecutionGuardStrictConfigPreservesZeroAndPositive(t *testing.T) {
	for _, tc := range []struct {
		text string
		want int
	}{{"", 0}, {"0", 0}, {"2", 2}, {"17", 17}} {
		text := validGoAccessRuntimeConfigYAML("admission-worker")
		if tc.text != "" {
			text += "  scheduler:\n    active_execution_limit: " + tc.text + "\n"
		}
		cfg, err := Load(writeConfig(t, text))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.PhaseTwo.Scheduler.ActiveExecutionLimit != tc.want {
			t.Fatalf("value=%s resolved=%d want=%d", tc.text, cfg.PhaseTwo.Scheduler.ActiveExecutionLimit, tc.want)
		}
	}
	for _, value := range []string{"-1", "true", "[]", `"unlimited"`} {
		text := validGoAccessRuntimeConfigYAML("admission-worker") + "  scheduler:\n    active_execution_limit: " + value + "\n"
		if _, err := Load(writeConfig(t, text)); err == nil {
			t.Fatalf("invalid guard %s accepted", value)
		}
	}
}
