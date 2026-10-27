// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package simulation

import (
	"encoding/json"
	"slices"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/policy"
)

func TestModelScopeMergeSimulationKeepsOnlyMatchingModels(t *testing.T) {
	var spec map[string]any
	if err := json.Unmarshal(mergeSpec(false), &spec); err != nil {
		t.Fatal(err)
	}
	spec["model_id"], spec["target_descriptor"] = "cw-Host", nil
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	events := []*domain.Event{
		event("other-db", "foreign-a", "foreign-db", "db", 0, domain.EventActionTriggered),
		event("host-db", "a", "db", "db", 1, domain.EventActionTriggered),
		event("other-app", "foreign-b", "foreign-app", "app", 2, domain.EventActionTriggered),
		event("host-app", "b", "app", "app", 3, domain.EventActionTriggered),
	}
	for i, e := range events {
		model := "cw-Host"
		if i%2 == 0 {
			model = "cw-Service"
		}
		e.Labels["model_id"] = domain.NewStringScalar(model)
		delete(e.Labels, "model_inst_id")
	}
	response := run(t, request(policy.Merge, raw, events...))
	last := response.Steps[3]
	if len(last.Windows) != 1 || last.Windows[0].Outcome != "succeeded" || len(last.Windows[0].Members) != 2 {
		t.Fatalf("model-only merge failed %+v", last)
	}
	for _, step := range response.Steps {
		for _, alert := range step.Alerts {
			model, _ := alert.Labels["model_id"].StringValue()
			if model == "cw-Service" && slices.Contains(last.Windows[0].Members, alert.AlertID) {
				t.Fatal("foreign model entered merge window")
			}
		}
	}
}
