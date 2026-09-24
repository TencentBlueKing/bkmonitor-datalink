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
	// The comparison is against the container this process was given, not
	// against Default. ReferenceContainer exists so that Default is the same
	// answer on every machine - a build agent's core count is not a product
	// decision - but Load is deliberately the other thing: its whole contract
	// is to size from the container the process actually got, which is why the
	// permits and queues beside this value already differ between the two. The
	// guard only ever matched Default because it used to be a constant zero on
	// both paths, and that is the property this change removes.
	container := DeriveScheduler(DetectCapacityInputs())
	if cfg.PhaseTwo.Scheduler.ActiveExecutionLimit != container.ActiveExecutions {
		t.Fatalf("resolved execution guard = %d, want the value derived from this container, %d",
			cfg.PhaseTwo.Scheduler.ActiveExecutionLimit, container.ActiveExecutions)
	}
	// Machine-independent half: whatever container this ran on, the resolved
	// guard and the resolved permits have to have come from the same
	// derivation. A file that reached either one alone would break this.
	if cfg.PhaseTwo.Scheduler.ActiveExecutionLimit !=
		cfg.PhaseTwo.Scheduler.ProcessQueryPermits*activeExecutionsPerQueryPermit {
		t.Fatalf("resolved guard %d and resolved permits %d did not come from one derivation",
			cfg.PhaseTwo.Scheduler.ActiveExecutionLimit, cfg.PhaseTwo.Scheduler.ProcessQueryPermits)
	}
}
