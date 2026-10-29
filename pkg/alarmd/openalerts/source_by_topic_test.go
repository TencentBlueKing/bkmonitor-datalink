// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package openalerts

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// topicSource is one event source as the link's control plane defines it,
// in its own shape: the Kafka subscription is under spec.storage.kafka, and
// the security block carries a credential this process must never keep.
type topicSource struct {
	topic     string
	enabled   *bool
	published int64
	deleted   bool
	fail      bool
	// answers is the id the definition says it is, when not its own.
	answers string
}

func yes() *bool { v := true; return &v }
func no() *bool  { v := false; return &v }

// topicServer is a Console whose one target names listed and covers the
// sources given; each source's definition is served from sources.
func topicServer(t *testing.T, listed string, sources map[string]*topicSource, topic string) (*HTTPReconciler, *sync.Map) {
	t.Helper()
	reads := &sync.Map{}
	names := make([]string, 0, len(sources))
	for name := range sources {
		names = append(names, name)
	}
	target := testBinding()
	target.EventSourceID, target.Sources = listed, names
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/strategy-index/targets") {
			_ = json.NewEncoder(w).Encode([]TargetBinding{target})
			return
		}
		id, ok := strings.CutPrefix(r.URL.Path, testLinkdAPIRoot+"/event-sources/")
		source := sources[id]
		if !ok || source == nil {
			http.NotFound(w, r)
			return
		}
		count, _ := reads.LoadOrStore(id, new(int))
		*count.(*int)++
		if source.fail {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		spec := map[string]any{"fingerprint_mode": "field", "fingerprint_field": "source_alert_id",
			"storage": map[string]any{"type": "kafka", "kafka": map[string]any{"brokers": []string{"198.51.100.7:9092"}, "topic": source.topic,
				"consumer_group": "linkd-" + id, "security": map[string]any{"protocol": "sasl_plaintext",
					"sasl": map[string]any{"mechanism": "plain", "username": "never-kept-user", "password": "never-kept-secret"}}}}}
		if source.enabled != nil {
			spec["enabled"] = *source.enabled
		}
		answered := id
		if source.answers != "" {
			answered = source.answers
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": answered, "revision": source.published, "published": source.published, "deleted": source.deleted, "spec": spec})
	}))
	t.Cleanup(server.Close)
	reader, err := NewHTTPReconciler(HTTPReconcilerOptions{BaseURL: server.URL + testLinkdAPIRoot, Client: server.Client(), Username: "user", Password: "secret",
		Index: testIndex(), MaxResponseBytes: 1 << 20, OutputTopic: topic})
	if err != nil {
		t.Fatal(err)
	}
	return reader, reads
}

// The link names a target after the first source that writes to its Redis,
// so a target shared by a source that receives nothing and the source that
// reads this deployment's topic can carry the wrong name. The source this
// deployment's events arrive under is the one candidate that is released,
// enabled, not deleted and subscribed to its output topic; the binding takes
// it, and the record says how it was chosen without any of the
// subscription's credentials.
func TestTheEventSourceIsTheCandidateReadingTheOutputTopic(t *testing.T) {
	reader, _ := topicServer(t, "legacy", map[string]*topicSource{
		"legacy":      {topic: "legacy_events", enabled: yes(), published: 10},
		"built_in_bk": {topic: "alarmd_event", enabled: yes(), published: 4},
	}, "alarmd_event")
	binding, err := reader.Binding(consoleCtx())
	if err != nil {
		t.Fatal(err)
	}
	if binding.EventSourceID != "built_in_bk" {
		t.Fatalf("event source %q, want built_in_bk: the one reading alarmd_event", binding.EventSourceID)
	}
	choice := reader.Record().SourceByTopic
	if choice.Outcome != SourceByTopicResolved || choice.EventSourceID != "built_in_bk" || choice.Listed != "legacy" || choice.Topic != "alarmd_event" || len(choice.Candidates) != 2 {
		t.Fatalf("choice %+v", choice)
	}
	encoded, _ := json.Marshal(reader.Record())
	if strings.Contains(string(encoded), "never-kept") {
		t.Fatalf("a subscription credential was kept: %s", encoded)
	}
	// The keying is read for the source chosen, and the reconcile asks for it.
	keying, err := reader.EventSource(consoleCtx())
	if err != nil || keying.EventSourceID != "built_in_bk" {
		t.Fatalf("keying %+v err %v, want built_in_bk's", keying, err)
	}
}

// None, or more than one, candidate reading the topic is a gap named with
// the candidates, not a guess: the binding keeps the source it had -- the one
// chosen before, while it is still a candidate, else the link's own name.
// A candidate disabled, never released or deleted does not count, and one
// whose definition could not be read leaves the choice open.
func TestAnUnresolvedSourceIsNamedAndChangesNothing(t *testing.T) {
	for _, tc := range []struct {
		name    string
		sources map[string]*topicSource
		reason  string
	}{
		{"none reads it", map[string]*topicSource{"legacy": {topic: "legacy_events", enabled: yes(), published: 10}, "other": {topic: "other_events", published: 2}}, SourceReasonNone},
		{"several read it", map[string]*topicSource{"legacy": {topic: "alarmd_event", enabled: yes(), published: 10}, "other": {topic: "alarmd_event", published: 2}}, SourceReasonSeveral},
		{"the reader is disabled", map[string]*topicSource{"legacy": {topic: "legacy_events", published: 10}, "built_in_bk": {topic: "alarmd_event", enabled: no(), published: 4}}, SourceReasonNone},
		{"the reader is not released", map[string]*topicSource{"legacy": {topic: "legacy_events", published: 10}, "built_in_bk": {topic: "alarmd_event", published: 0}}, SourceReasonNone},
		{"the reader is deleted", map[string]*topicSource{"legacy": {topic: "legacy_events", published: 10}, "built_in_bk": {topic: "alarmd_event", published: 4, deleted: true}}, SourceReasonNone},
		{"a definition is unreadable", map[string]*topicSource{"legacy": {fail: true}, "built_in_bk": {topic: "alarmd_event", published: 4}}, SourceReasonUnreadable},
		{"a definition answers for another source", map[string]*topicSource{"legacy": {topic: "legacy_events", published: 10}, "built_in_bk": {topic: "alarmd_event", published: 4, answers: "other"}}, SourceReasonUnreadable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader, _ := topicServer(t, "legacy", tc.sources, "alarmd_event")
			binding, err := reader.Binding(consoleCtx())
			if err != nil {
				t.Fatal(err)
			}
			choice := reader.Record().SourceByTopic
			if binding.EventSourceID != "legacy" || choice.Outcome != SourceByTopicUnresolved || choice.Reason != tc.reason || len(choice.Candidates) != len(tc.sources) {
				t.Fatalf("binding %q choice %+v, want the link's own name kept and %s", binding.EventSourceID, choice, tc.reason)
			}
		})
	}

	// Once chosen, an unreadable refresh keeps the choice.
	sources := map[string]*topicSource{"legacy": {topic: "legacy_events", published: 10}, "built_in_bk": {topic: "alarmd_event", published: 4}}
	reader, _ := topicServer(t, "legacy", sources, "alarmd_event")
	if binding, err := reader.Binding(consoleCtx()); err != nil || binding.EventSourceID != "built_in_bk" {
		t.Fatalf("binding %+v err %v", binding, err)
	}
	sources["built_in_bk"].fail = true
	binding, err := reader.resolve(consoleCtx())
	if err != nil || binding.EventSourceID != "built_in_bk" || reader.Record().SourceByTopic.Outcome != SourceByTopicUnresolved {
		t.Fatalf("binding %+v err %v choice %+v, want the earlier choice kept while unresolved", binding, err, reader.Record().SourceByTopic)
	}
}

// Without an output topic nothing is asked: the binding is the link's.
func TestWithoutAnOutputTopicTheLinksNameStands(t *testing.T) {
	reader, reads := topicServer(t, "legacy", map[string]*topicSource{
		"legacy": {topic: "legacy_events", published: 10}, "built_in_bk": {topic: "alarmd_event", published: 4},
	}, "")
	binding, err := reader.Binding(consoleCtx())
	if err != nil || binding.EventSourceID != "legacy" {
		t.Fatalf("binding %+v err %v", binding, err)
	}
	reads.Range(func(key, value any) bool {
		t.Fatalf("read %v %d times with no topic to match", key, *value.(*int))
		return true
	})
	if reader.Record().SourceByTopic.Outcome != SourceByTopicNotAsked {
		t.Fatalf("choice %+v", reader.Record().SourceByTopic)
	}
}

// A reconciliation asks for the chosen source and answers with it: the
// alerts this deployment's events opened, under built_in_bk, are its own,
// which is what lets the closes judge them; another source's stay foreign.
func TestTheReconciliationIsForTheChosenSource(t *testing.T) {
	sources := map[string]*topicSource{"legacy": {topic: "legacy_events", published: 10}, "built_in_bk": {topic: "alarmd_event", published: 4}}
	var asked string
	target := testBinding()
	target.EventSourceID, target.Sources = "built_in_bk", []string{"built_in_bk", "legacy"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/strategy-index/targets"):
			listed := target
			listed.EventSourceID = "legacy"
			_ = json.NewEncoder(w).Encode([]TargetBinding{listed})
		case strings.HasSuffix(r.URL.Path, "/strategy-index/reconcile"):
			asked = r.URL.Query().Get("event_source_id")
			body := reconciliationJSON()
			body["target"] = target
			body["rows"] = []any{map[string]any{"fingerprint": "f1", "status": "matched", "alerts": []Alert{
				{AlertID: "a1", EventSourceID: "built_in_bk", Fingerprint: "f1"}, {AlertID: "a2", EventSourceID: "legacy", Fingerprint: "f1"}}}}
			_ = json.NewEncoder(w).Encode(body)
		default:
			id := strings.TrimPrefix(r.URL.Path, testLinkdAPIRoot+"/event-sources/")
			s := sources[id]
			_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "revision": s.published, "published": s.published,
				"spec": map[string]any{"fingerprint_mode": "field", "fingerprint_field": "source_alert_id", "storage": map[string]any{"kafka": map[string]any{"topic": s.topic}}}})
		}
	}))
	t.Cleanup(server.Close)
	reader, err := NewHTTPReconciler(HTTPReconcilerOptions{BaseURL: server.URL + testLinkdAPIRoot, Client: server.Client(), Username: "user", Password: "secret",
		Index: testIndex(), MaxResponseBytes: 1 << 20, OutputTopic: "alarmd_event"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := reader.Reconcile(consoleCtx(), keyA)
	if err != nil {
		t.Fatal(err)
	}
	if asked != "built_in_bk" || result.EventSourceID != "built_in_bk" {
		t.Fatalf("asked %q answered %q, want built_in_bk", asked, result.EventSourceID)
	}
	own := 0
	for _, alert := range result.Alerts {
		if alert.EventSourceID == result.EventSourceID {
			own++
		}
	}
	if own != 1 {
		t.Fatal(fmt.Sprintf("%d own alerts of %d, want the built_in_bk one", own, len(result.Alerts)))
	}
}
