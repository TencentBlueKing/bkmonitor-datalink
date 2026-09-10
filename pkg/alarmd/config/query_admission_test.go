package config

import "testing"

// The complete-Runner guard is part of the product capacity profile, not an
// environment knob: it interacts with the permit count and the queue depth,
// all three of which the process derives from the same container. A file that
// states it is refused rather than obeyed, and the value the process runs
// under stays the one it derived.
func TestExecutionGuardIsNotAnEnvironmentKnob(t *testing.T) {
	for _, written := range []string{"0", "2", "17", "-1", "true", `"unlimited"`} {
		text := validGoAccessRuntimeConfigYAML("admission-worker") +
			"  scheduler:\n    active_execution_limit: " + written + "\n"
		if _, err := Load(writeConfig(t, text)); err == nil {
			t.Fatalf("written execution guard %s accepted", written)
		}
	}

	cfg, err := Load(writeConfig(t, validGoAccessRuntimeConfigYAML("admission-worker")))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PhaseTwo.Scheduler.ActiveExecutionLimit != Default().PhaseTwo.Scheduler.ActiveExecutionLimit {
		t.Fatalf("resolved execution guard = %d, want the product value %d",
			cfg.PhaseTwo.Scheduler.ActiveExecutionLimit, Default().PhaseTwo.Scheduler.ActiveExecutionLimit)
	}
}
