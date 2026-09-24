// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package openalerts

import (
	"context"
	"errors"
	"net/url"
)

// EventSourceKeying is how the link keys the alerts of this deployment's
// event source: the fingerprint mode and the field or fields it is taken
// from, as the link's control plane holds the definition. It is what a
// recovery lookup's key has to agree with, so it is read from the link
// rather than assumed. Revision and Published are the definition's edit
// revision and the release that is live; Pending says a release is waiting.
//
// The definition read is the edit version, not the release that runs. The
// two agree on keying: the link refuses any change to the fingerprint
// settings once the source has been released, since it cannot migrate the
// keys of alerts already open. The one case with nothing running is a
// source never released (Published 0), and InEffect says so.
//
// KeyedByAlertID is the question a reader actually asks: whether the link
// keys these alerts by the alert id this deployment sends. It does exactly
// in field mode on source_alert_id; in fields mode the key is a hash of the
// named fields, which differs from the alert id even when source_alert_id
// is the only field named.
type EventSourceKeying struct {
	EventSourceID     string   `json:"event_source_id"`
	FingerprintMode   string   `json:"fingerprint_mode"`
	FingerprintField  string   `json:"fingerprint_field,omitempty"`
	FingerprintFields []string `json:"fingerprint_fields,omitempty"`
	Revision          int64    `json:"revision"`
	Published         int64    `json:"published"`
	Pending           bool     `json:"pending,omitempty"`
	Deleted           bool     `json:"deleted,omitempty"`
	InEffect          bool     `json:"in_effect"`
	KeyedByAlertID    bool     `json:"keyed_by_alert_id"`
}

// The link's fingerprint modes and the field that carries the alert id.
const (
	FingerprintModeField  = "field"
	FingerprintModeFields = "fields"
	sourceAlertIDField    = "source_alert_id"
)

// ErrEventSourceMismatch is an answer about another event source than the
// target's: not a reading of ours.
var ErrEventSourceMismatch = errors.New("the Console answered for another event source")

func (keying EventSourceKeying) clone() EventSourceKeying {
	keying.FingerprintFields = append([]string(nil), keying.FingerprintFields...)
	return keying
}

// ErrNoEventSource is a resolved target that names no event source.
var ErrNoEventSource = errors.New("the target's event source is not known yet")

// EventSource reads this deployment's event source definition from the
// link's Console and keeps it on the record. The Console proxies its
// control plane's read of the definition; nothing is written.
func (reader *HTTPReconciler) EventSource(ctx context.Context) (EventSourceKeying, error) {
	keying, err := reader.eventSource(ctx)
	at := reader.now()
	reader.calls.record(ConsoleOpEventSource, at, err)
	if err == nil {
		reader.calls.mu.Lock()
		reader.calls.eventSource, reader.calls.eventSourceReadAt = keying.clone(), at
		reader.calls.mu.Unlock()
	}
	return keying, err
}

func (reader *HTTPReconciler) eventSource(ctx context.Context) (EventSourceKeying, error) {
	target, err := reader.Binding(ctx)
	if err != nil {
		return EventSourceKeying{}, err
	}
	if target.EventSourceID == "" {
		return EventSourceKeying{}, ErrNoEventSource
	}
	var response struct {
		ID        string `json:"id"`
		Revision  int64  `json:"revision"`
		Published int64  `json:"published"`
		Deleted   bool   `json:"deleted"`
		Pending   *struct {
			Version int64 `json:"version"`
		} `json:"pending"`
		Spec struct {
			FingerprintMode   string   `json:"fingerprint_mode"`
			FingerprintField  string   `json:"fingerprint_field"`
			FingerprintFields []string `json:"fingerprint_fields"`
		} `json:"spec"`
	}
	if err := reader.getPath(ctx, "/local-api/event-sources/"+url.PathEscape(target.EventSourceID), nil, &response); err != nil {
		return EventSourceKeying{}, err
	}
	if response.ID != target.EventSourceID {
		return EventSourceKeying{}, ErrEventSourceMismatch
	}
	keying := EventSourceKeying{EventSourceID: target.EventSourceID, FingerprintMode: response.Spec.FingerprintMode,
		FingerprintField: response.Spec.FingerprintField, FingerprintFields: response.Spec.FingerprintFields,
		Revision: response.Revision, Published: response.Published, Pending: response.Pending != nil, Deleted: response.Deleted}
	keying.InEffect = keying.Published > 0 && !keying.Deleted
	keying.KeyedByAlertID = keying.InEffect && keying.FingerprintMode == FingerprintModeField && keying.FingerprintField == sourceAlertIDField
	return keying, nil
}
