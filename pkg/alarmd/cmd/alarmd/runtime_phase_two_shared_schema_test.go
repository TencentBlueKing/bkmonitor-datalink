package main

import (
	"testing"
)

func TestRuntimeReadsBackEffectiveSharedSchemaScope(t *testing.T) {
	facts := phaseTwoUQCodecFacts()
	if facts.Mode != "shared_schema" || facts.Scope != "ordinary_execute_non_polling" {
		t.Fatal("default negotiation scope is not reported", facts)
	}
}
