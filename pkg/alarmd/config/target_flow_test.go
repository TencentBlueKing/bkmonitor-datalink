package config

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"testing"
)

func TestTargetFlowConfigurationValidation(t *testing.T) {
	c := validGoAccessConfigObject()
	if len(c.PhaseTwo.TargetFlow.QueryGroups) != 0 {
		t.Fatal("default must be disabled")
	}
	c.PhaseTwo.TargetFlow = observability.TargetFlowConfig{QueryGroups: []string{"bad"}}
	if c.PhaseTwo.validate() == nil {
		t.Fatal("invalid target not rejected by real config validation")
	}
	c.PhaseTwo.TargetFlow.QueryGroups = []string{"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}
	if e := c.PhaseTwo.TargetFlow.Validate(); e != nil {
		t.Fatal(e)
	}
}
