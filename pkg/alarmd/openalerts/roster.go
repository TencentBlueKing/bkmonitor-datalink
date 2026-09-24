// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package openalerts

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// RosterPage is one page of the alert link's own list of the strategies it
// holds an open alert set for, read through its Console (strategy-index
// browse). The link walks its key space on its own connection to produce it;
// this process only follows the cursor.
//
// A page is not a snapshot. The link says so itself: pages may repeat a
// strategy, and a strategy being rebuilt may be missing from every page of a
// walk. Both errors only ever make the roster smaller or repeat an entry,
// never invent one, which is the direction a caller deciding what to close
// can afford.
type RosterPage struct {
	Rows []RosterRow
	// Next is the cursor for the following page; empty when the walk is
	// complete.
	Next   string
	Health LinkHealth
}

// RosterRow is one strategy the link lists. Members is nil when the link
// could not read that strategy's set; a nil is "not read", never "empty".
type RosterRow struct {
	TenantID   string
	StrategyID string
	Members    *int
	Pending    bool
	Error      string
}

// LinkHealth is the link's own account of the process that maintains the
// sets: when a full discovery last succeeded, whether the last one failed,
// and how much is queued for refresh. It is what the link publishes about
// itself, beside the data, in the same response.
type LinkHealth struct {
	LastSuccess  time.Time
	LastAttempt  time.Time
	Error        string
	PendingCount int
}

// RosterPageSize is how many keys a browse page asks the link to scan per
// step. The link's own bounds are 10 to 200; the largest is asked for so
// that a walk takes as few requests as the link allows.
const RosterPageSize = 200

// roster is Roster without the call record.
func (reader *HTTPReconciler) roster(ctx context.Context, cursor string) (RosterPage, error) {
	b, err := reader.Binding(ctx)
	if err != nil {
		return RosterPage{}, err
	}
	query := url.Values{"event_source_id": {b.EventSourceID}, "hook_name": {b.HookName}, "count": {strconv.Itoa(RosterPageSize)}}
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	var response struct {
		Target     TargetBinding `json:"target"`
		NextCursor *string       `json:"nextCursor"`
		Phase      string        `json:"phase"`
		Health     *struct {
			LastSuccess  *string `json:"lastSuccess"`
			LastAttempt  *string `json:"lastAttempt"`
			Error        *string `json:"error"`
			PendingCount *int    `json:"pendingCount"`
		} `json:"health"`
		Rows []struct {
			TenantID   string  `json:"tenantId"`
			StrategyID string  `json:"strategyId"`
			Key        string  `json:"key"`
			Members    *int    `json:"members"`
			Pending    *bool   `json:"pending"`
			Error      *string `json:"error"`
		} `json:"rows"`
	}
	if err := reader.get(ctx, "browse", query, &response); err != nil {
		return RosterPage{}, err
	}
	// The response names the target it walked. A walk of another target -
	// another prefix, another database - would hand this process another
	// deployment's strategies.
	if !sameTarget(b, response.Target) {
		return RosterPage{}, errors.New("alarmd openalerts: roster target differs from the binding")
	}
	if response.Health == nil || response.Rows == nil || response.Health.PendingCount == nil ||
		(response.Phase != "sets" && response.Phase != "pending") {
		return RosterPage{}, ErrIncomplete
	}
	page := RosterPage{Rows: make([]RosterRow, 0, len(response.Rows))}
	if response.NextCursor != nil {
		page.Next = *response.NextCursor
	}
	if page.Health.LastSuccess, err = optionalTime(response.Health.LastSuccess); err != nil {
		return RosterPage{}, err
	}
	if page.Health.LastAttempt, err = optionalTime(response.Health.LastAttempt); err != nil {
		return RosterPage{}, err
	}
	if response.Health.Error != nil {
		page.Health.Error = *response.Health.Error
	}
	page.Health.PendingCount = *response.Health.PendingCount
	for _, row := range response.Rows {
		key := StrategyKey{TenantID: row.TenantID, StrategyID: row.StrategyID}
		if !validStrategyKey(key) || row.Key != b.KeyPrefix+":"+row.TenantID+":"+row.StrategyID {
			return RosterPage{}, ErrIncomplete
		}
		entry := RosterRow{TenantID: row.TenantID, StrategyID: row.StrategyID, Members: row.Members}
		if row.Pending != nil {
			entry.Pending = *row.Pending
		}
		if row.Error != nil {
			entry.Error = *row.Error
		}
		page.Rows = append(page.Rows, entry)
	}
	return page, nil
}

func optionalTime(value *string) (time.Time, error) {
	if value == nil || *value == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, *value)
	if err != nil {
		return time.Time{}, ErrIncomplete
	}
	return parsed, nil
}

// AlertRecord is what the link's own alert record says about one alert:
// whose it is, whether it is still active, which strategy produced it and
// under which business and revision. These are the labels the alert was
// created with, which is the only place the identity of a strategy that no
// longer exists is still written down.
type AlertRecord struct {
	AlertID       string
	TenantID      string
	EventSourceID string
	Fingerprint   string
	Status        string
	Severity      string
	StrategyID    string
	BusinessID    int64
	Revision      int64
}

// ErrAlertNotFound is the link answering that it has no such alert.
var ErrAlertNotFound = errors.New("alarmd openalerts: alert not found")

// alertRecord is AlertRecord without the call record.
func (reader *HTTPReconciler) alertRecord(ctx context.Context, tenantID, alertID string) (AlertRecord, error) {
	if tenantID == "" || alertID == "" || len(alertID) > 1024 || len(tenantID) > 256 {
		return AlertRecord{}, errors.New("alarmd openalerts: invalid alert identity")
	}
	var response struct {
		TenantID string `json:"tenantId"`
		ID       string `json:"id"`
		Payload  *struct {
			AlertID       string                     `json:"alert_id"`
			TenantID      string                     `json:"bk_tenant_id"`
			EventSourceID string                     `json:"event_source_id"`
			Fingerprint   string                     `json:"fingerprint"`
			Status        string                     `json:"status"`
			Severity      string                     `json:"severity"`
			Labels        map[string]json.RawMessage `json:"labels"`
		} `json:"payload"`
	}
	err := reader.getPath(ctx, "/local-api/alerts/"+url.PathEscape(alertID), url.Values{"bk_tenant_id": {tenantID}}, &response)
	var status statusError
	if errors.As(err, &status) && int(status) == http.StatusNotFound {
		return AlertRecord{}, ErrAlertNotFound
	}
	if err != nil {
		return AlertRecord{}, err
	}
	p := response.Payload
	if p == nil || response.TenantID != tenantID || response.ID != alertID || p.AlertID != alertID || p.TenantID != tenantID {
		return AlertRecord{}, ErrIncomplete
	}
	record := AlertRecord{AlertID: p.AlertID, TenantID: p.TenantID, EventSourceID: p.EventSourceID,
		Fingerprint: p.Fingerprint, Status: p.Status, Severity: p.Severity}
	record.StrategyID, _ = labelText(p.Labels["strategy_id"])
	record.BusinessID, _ = labelInteger(p.Labels["bk_biz_id"])
	record.Revision, _ = labelInteger(p.Labels["strategy_version"])
	return record, nil
}

// labelText reads a label the way the link's index does: a string as it is,
// a number in plain decimal. Anything else is not a strategy label.
func labelText(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text, text != ""
	}
	var number json.Number
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if decoder.Decode(&number) != nil {
		return "", false
	}
	if _, err := strconv.ParseInt(number.String(), 10, 64); err != nil {
		return "", false
	}
	return number.String(), true
}

// labelInteger reads an integer label written either as a number or as its
// decimal text. Zero means absent: none of the labels read this way has a
// meaningful zero.
func labelInteger(raw json.RawMessage) (int64, bool) {
	text, ok := labelText(raw)
	if !ok {
		return 0, false
	}
	value, err := strconv.ParseInt(text, 10, 64)
	if err != nil || value == 0 {
		return 0, false
	}
	return value, true
}
