// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package policy

import (
	"encoding/json"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/store/storetest"
)

func TestObjectFallbackAfterEnrichFailure(t *testing.T) {
	for _, tc := range []struct {
		name, processors, explicit, subjectID, subjectName, want string
	}{
		{name: "resource failed", processors: `[{"resource":{"status":"failed","patches":[]}}]`, subjectID: "stable-service", want: "stable-service"},
		{name: "resource partial", processors: `[{"resource":{"status":"partial","patches":[]}}]`, subjectID: "stable-service", want: "stable-service"},
		{name: "failed display preserves current name", processors: `[{"display":{"status":"failed","patches":[]}}]`, subjectID: "stable-service", subjectName: "current name", want: "current name"},
		{name: "empty identity stays empty", processors: `[{"resource":{"status":"failed","patches":[]}}]`},
		{name: "successful display overrides failed resource", processors: `[{"resource":{"status":"failed","patches":[]}},{"display":{"status":"succeeded","patches":[{"op":"set","path":"$.subject_name","value":"known name"}]}}]`, subjectID: "stable-service", want: "known name"},
		{name: "explicit empty object", processors: `[{"resource":{"status":"failed","patches":[]}}]`, explicit: `""`, subjectID: "stable-service"},
		{name: "explicit object", processors: `[{"display":{"status":"failed","patches":[]}}]`, explicit: `"explicit"`, subjectID: "stable-service", want: "explicit"},
		{name: "explicit object survives failure", processors: `[{"fields":{"status":"failed","patches":[]}}]`, explicit: `"explicit"`, subjectID: "stable-service", want: "explicit"},
		{name: "unknown failed processor preserves identity", processors: `[{"unknown":{"status":"failed","patches":[]}}]`, subjectID: "stable-service", want: "stable-service"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			event := policyEvent(tc.processors, domain.EnrichStatusPartial)
			event.SubjectID, event.SubjectName = tc.subjectID, tc.subjectName
			if tc.explicit != "" {
				event.ExtraData["object"] = json.RawMessage(tc.explicit)
			}
			views := map[string]*FactView{"event": mustEventView(t, event, nil)}
			alert := storetest.Alert("tenant", "a1", "e1", "f1", "warning")
			alert.SubjectID, alert.SubjectName, alert.ExtraData = event.SubjectID, event.SubjectName, event.ExtraData
			alert.EnrichStatus, alert.Enrich = event.EnrichStatus, event.Enrich.Evaluations[0].Data
			view, err := AlertView(alert, nil, nil, RelationContext{})
			if err != nil {
				t.Fatal(err)
			}
			views["alert"] = view
			for name, view := range views {
				got, err := view.Field(t.Context(), "object")
				if err != nil || !got.Present || got.Data != tc.want {
					t.Fatalf("%s: got %+v, err=%v, want %q", name, got, err, tc.want)
				}
			}
		})
	}
}

func TestObjectGroupingAfterEnrichFailure(t *testing.T) {
	var spec map[string]any
	if err := json.Unmarshal(testSpec("object grouping"), &spec); err != nil {
		t.Fatal(err)
	}
	spec["scheme"] = []Scheme{{Type: "aggregation", Duration: 12, DurationType: "hour", Fields: []string{"object"}}}
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := Compile(Suppression, raw)
	if err != nil {
		t.Fatal(err)
	}
	want, err := GroupKey([]any{"stable-service"})
	if err != nil {
		t.Fatal(err)
	}
	for _, processor := range []string{"resource", "display"} {
		event := policyEvent(`[{"`+processor+`":{"status":"failed","patches":[]}}]`, domain.EnrichStatusPartial)
		event.SubjectID = "stable-service"
		view := mustEventView(t, event, nil)
		key, reason, err := EvaluateGrouping(t.Context(), compiled, view)
		if err != nil || reason != "" || key != want {
			t.Fatalf("%s: group=%q reason=%q err=%v", processor, key, reason, err)
		}
		if value, err := view.Field(t.Context(), "model_id"); err != nil || value.Present {
			t.Fatalf("model must remain missing: %+v %v", value, err)
		}
	}
}
