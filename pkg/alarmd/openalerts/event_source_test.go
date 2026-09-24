// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package openalerts

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
)

// The link's definition of this deployment's event source is read in the
// control plane's own shape, through the Console's read-only proxy, for the
// target's own event source; the keying lands on the Console's record with
// the call counted. A failed read is a failed call and leaves the last
// reading as it was.
func TestTheEventSourceKeyingIsReadAndKept(t *testing.T) {
	fail := false
	reader := rosterServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/local-api/event-sources/"+testBinding().EventSourceID {
			t.Errorf("request %s %s", r.Method, r.URL.Path)
		}
		if fail {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": testBinding().EventSourceID, "revision": 7, "published": 6, "deleted": false,
			"pending": map[string]any{"version": 7},
			"spec":    map[string]any{"fingerprint_mode": "fields", "fingerprint_fields": []string{"strategy_id", "dimensions"}}})
	})
	keying, err := reader.EventSource(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := EventSourceKeying{EventSourceID: testBinding().EventSourceID, FingerprintMode: "fields",
		FingerprintFields: []string{"strategy_id", "dimensions"}, Revision: 7, Published: 6, Pending: true}
	if keying.EventSourceID != want.EventSourceID || keying.FingerprintMode != "fields" || len(keying.FingerprintFields) != 2 ||
		keying.Revision != 7 || keying.Published != 6 || !keying.Pending {
		t.Fatalf("keying = %+v, want %+v", keying, want)
	}
	record := reader.Record()
	if record.EventSourceReadAt.IsZero() || record.EventSource.FingerprintMode != "fields" || record.Calls[ConsoleOpEventSource].Calls != 1 {
		t.Fatalf("record = %+v", record)
	}
	fail = true
	if _, err := reader.EventSource(context.Background()); err == nil {
		t.Fatal("a failed read reported success")
	}
	record = reader.Record()
	call := record.Calls[ConsoleOpEventSource]
	if call.Failures != 1 || !call.LatestFailed || record.EventSource.FingerprintMode != "fields" {
		t.Fatalf("after a failure: call %+v keying %+v, want the failure counted and the last reading kept", call, record.EventSource)
	}
}

// Whether the link keys our alerts by the alert id we send, decided from
// the definition: field mode on source_alert_id does; fields mode hashes,
// even when source_alert_id is the only field; a source never released has
// no keying in effect; an answer about another source is not ours.
func TestWhetherTheLinkKeysOurAlertsByAlertID(t *testing.T) {
	for _, tc := range []struct {
		name            string
		id              string
		published       int64
		spec            map[string]any
		inEffect, keyed bool
		err             error
	}{
		{"field on source_alert_id", "", 3, map[string]any{"fingerprint_mode": "field", "fingerprint_field": "source_alert_id"}, true, true, nil},
		{"fields hashing only source_alert_id", "", 3, map[string]any{"fingerprint_mode": "fields", "fingerprint_fields": []string{"source_alert_id"}}, true, false, nil},
		{"fields mode with a leftover field", "", 3, map[string]any{"fingerprint_mode": "fields", "fingerprint_field": "source_alert_id",
			"fingerprint_fields": []string{"source_alert_id", "strategy_id"}}, true, false, nil},
		{"field on another field", "", 3, map[string]any{"fingerprint_mode": "field", "fingerprint_field": "dedupe_key"}, true, false, nil},
		{"never released", "", 0, map[string]any{"fingerprint_mode": "field", "fingerprint_field": "source_alert_id"}, false, false, nil},
		{"another source", "other", 3, map[string]any{"fingerprint_mode": "field", "fingerprint_field": "source_alert_id"}, false, false, ErrEventSourceMismatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := tc.id
			if id == "" {
				id = testBinding().EventSourceID
			}
			reader := rosterServer(t, func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "revision": 3, "published": tc.published, "spec": tc.spec})
			})
			keying, err := reader.EventSource(context.Background())
			if tc.err != nil {
				if !errors.Is(err, tc.err) {
					t.Fatalf("err = %v, want %v", err, tc.err)
				}
				return
			}
			if err != nil || keying.InEffect != tc.inEffect || keying.KeyedByAlertID != tc.keyed {
				t.Fatalf("keying %+v err %v, want in effect %v keyed by alert id %v", keying, err, tc.inEffect, tc.keyed)
			}
		})
	}
}
