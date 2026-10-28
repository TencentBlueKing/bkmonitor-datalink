// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package policy

import (
	"encoding/json"
	"errors"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/store/storetest"
)

func TestObjectFallbackAfterEnrichFailure(t *testing.T) {
	for _, tc := range []struct {
		name, processors, explicit, subjectID, subjectName, want string
		unavailable                                              bool
	}{
		{name: "resource failed", processors: `[{"resource":{"status":"failed","patches":[]}}]`, subjectID: "stable-service", want: "stable-service"},
		{name: "resource partial", processors: `[{"resource":{"status":"partial","patches":[]}}]`, subjectID: "stable-service", want: "stable-service"},
		{name: "failed display name is not trusted", processors: `[{"display":{"status":"failed","patches":[]}}]`, subjectID: "stable-service", subjectName: "stale name", want: "stable-service"},
		{name: "no stable identity", processors: `[{"resource":{"status":"failed","patches":[]}}]`, unavailable: true},
		{name: "successful display overrides failed resource", processors: `[{"resource":{"status":"failed","patches":[]}},{"display":{"status":"succeeded","patches":[{"op":"set","path":"$.subject_name","value":"known name"}]}}]`, subjectID: "stable-service", want: "known name"},
		{name: "explicit empty object", processors: `[{"resource":{"status":"failed","patches":[]}}]`, explicit: `""`, subjectID: "stable-service"},
		{name: "explicit object", processors: `[{"display":{"status":"failed","patches":[]}}]`, explicit: `"explicit"`, subjectID: "stable-service", want: "explicit"},
		{name: "explicit object unavailable cannot fall back", processors: `[{"fields":{"status":"failed","patches":[]}}]`, explicit: `"explicit"`, subjectID: "stable-service", unavailable: true},
		{name: "unknown failed processor remains unavailable", processors: `[{"unknown":{"status":"failed","patches":[]}}]`, subjectID: "stable-service", unavailable: true},
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
				if tc.unavailable {
					if !errors.Is(err, ErrUnavailable) {
						t.Fatalf("%s: got %+v, err=%v", name, got, err)
					}
					continue
				}
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
		if _, err := view.Field(t.Context(), "model_id"); processor == "resource" && !errors.Is(err, ErrUnavailable) {
			t.Fatalf("resource model became available: %v", err)
		}
	}
}
