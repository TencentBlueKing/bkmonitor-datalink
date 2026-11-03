// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package openalerts

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"slices"
	"strings"
)

// ConsoleOpAlertRead is an operator's read of the link's alerts, their logs
// and events (linkd.alerts): a read someone asked for, counted apart from
// the reads the closes make.
const ConsoleOpAlertRead = "alert_read"

// The link's records an operator reads, by the admin module's names.
const (
	EntityAlerts    = "alerts"
	EntityAlertLogs = "alert-logs"
	EntityEvents    = "events"
)

// LinkEntities are the records Entities reads.
var LinkEntities = []string{EntityAlerts, EntityAlertLogs, EntityEvents}

// EntityPage is one page of the link's records as the Console lists them:
// each item as the link wrote it ({tenantId, id, timestamp, summary,
// payload}), and the cursor of the next page, empty on the last.
type EntityPage struct {
	Items      []json.RawMessage `json:"items"`
	NextCursor string            `json:"next_cursor,omitempty"`
	Warnings   []string          `json:"warnings,omitempty"`
}

// Entities reads one page of the link's alerts, alert logs or events with
// the query as given: the time range, page size and filters are the link's
// to accept or refuse, and a refusal comes back with its words
// (LinkRefusal) rather than this process copying the link's bounds.
func (reader *HTTPReconciler) Entities(ctx context.Context, entity string, query url.Values) (EntityPage, error) {
	if err := requireDeadline(ctx); err != nil {
		return EntityPage{}, err
	}
	if !slices.Contains(LinkEntities, entity) {
		return EntityPage{}, errors.New("alarmd openalerts: not a record the link lists for an operator")
	}
	var response struct {
		Items      []json.RawMessage `json:"items"`
		NextCursor string            `json:"nextCursor"`
		Warnings   []string          `json:"warnings"`
	}
	err := reader.getPathAnswering(ctx, "/"+entity, query, &response, true)
	if err == nil && response.Items == nil {
		err = ErrIncomplete
	}
	reader.record(ConsoleOpAlertRead, reader.now(), err)
	if err != nil {
		return EntityPage{}, err
	}
	return EntityPage{Items: response.Items, NextCursor: response.NextCursor, Warnings: response.Warnings}, nil
}

// Alert reads one of the link's alerts on the route that addresses it by id,
// as the link wrote it ({tenantId, id, payload}). The link's list of alerts
// takes no alert id: asked with one, it answers every alert in range, page
// by page, so an alert asked for by id is read where the link addresses it.
// ErrAlertNotFound is the link having no such alert.
func (reader *HTTPReconciler) Alert(ctx context.Context, tenantID, alertID string) (json.RawMessage, error) {
	if err := requireDeadline(ctx); err != nil {
		return nil, err
	}
	if tenantID == "" || alertID == "" || len(alertID) > 1024 || len(tenantID) > 256 {
		return nil, errors.New("alarmd openalerts: invalid alert identity")
	}
	var item json.RawMessage
	err := reader.getPathAnswering(ctx, "/alerts/"+url.PathEscape(alertID), url.Values{"bk_tenant_id": {tenantID}}, &item, true)
	if errors.Is(err, errObjectMissing) {
		reader.record(ConsoleOpAlertRead, reader.now(), nil)
		return nil, ErrAlertNotFound
	}
	if err == nil {
		var identity struct {
			TenantID string `json:"tenantId"`
			ID       string `json:"id"`
		}
		if json.Unmarshal(item, &identity) != nil || identity.TenantID != tenantID || identity.ID != alertID {
			err = ErrIncomplete
		}
	}
	reader.record(ConsoleOpAlertRead, reader.now(), err)
	if err != nil {
		return nil, err
	}
	return item, nil
}

// LinkRefusal is the link answering other than 200 with its own words: the
// code and message of its error body, cut to maxRefusalBytes. It is a status
// failure (ConsoleFailureClass) like any other.
type LinkRefusal struct {
	Status  int
	Code    string
	Message string
}

const maxRefusalBytes = 512

func (refusal *LinkRefusal) Error() string {
	text := statusError(refusal.Status).Error()
	if refusal.Code != "" || refusal.Message != "" {
		text += ": " + strings.TrimSpace(refusal.Code+" "+refusal.Message)
	}
	return text
}

func (refusal *LinkRefusal) Unwrap() error { return statusError(refusal.Status) }

func linkRefusal(status int, body io.Reader) error {
	var answer struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	data, _ := io.ReadAll(io.LimitReader(body, 16<<10))
	_ = json.Unmarshal(data, &answer)
	message := answer.Error.Message
	if message == "" {
		message = answer.Message
	}
	return &LinkRefusal{Status: status, Code: cut(answer.Error.Code, 64), Message: cut(message, maxRefusalBytes)}
}

// cut shortens text to at most limit bytes on a character boundary.
func cut(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	return strings.ToValidUTF8(text[:limit], "")
}
