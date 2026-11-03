// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package linkdmanage is the alert link's management API as alarmd's
// control leader calls it: the alert search, the close by filter and the
// close of one alert by its id (Linkd b7e5beddf
// docs/reference/contracts/alert-search.md and alert-close.md). Each call
// carries a fresh Internal-Token, an HS256 JWT signed with the link's
// dispatch secret. A client built without a secret sends no Internal-Token
// header at all rather than one with an empty value; the link has no mode
// without a token, so it answers such a call 401, and a deployment that
// states the link does not start without the secret
// (config.ErrLinkdControlPlaneRequired).
//
// The package says what the link answered and nothing more: a status the
// link gave is a *StatusError, a per-item result is the link's own fields.
// What to decide from them is the caller's (package filterclose).
package linkdmanage

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// The link's own bounds, transcribed (alert-search.md, alert-close.md):
// one page holds 1..500 alerts, a filtered request body at most 1 MiB by
// default (dispatch.alert_search.max_request_bytes), a single close body
// 4096 bytes, a search page at most 8 MiB by default.
const (
	MaxLimit            = 500
	MaxFilterBodyBytes  = 1 << 20
	MaxCloseBodyBytes   = 4096
	defaultMaxPageBytes = 8 << 20
)

// Client calls the link's management routes.
type Client struct {
	base     *url.URL
	client   *http.Client
	secret   []byte
	username string
	maxBytes int64
	now      func() time.Time
}

// Options configure the client. Secret is the link's dispatch.jwt.secret_key,
// read from the file the deployment names; empty sends no token header. MaxBytes bounds one answer read;
// zero is the link's own page bound plus room for the envelope.
type Options struct {
	BaseURL  string
	Secret   string
	Username string
	Client   *http.Client
	MaxBytes int64
	Now      func() time.Time
}

func New(options Options) (*Client, error) {
	base, err := url.Parse(strings.TrimRight(options.BaseURL, "/"))
	if err != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") || base.User != nil || base.RawQuery != "" {
		return nil, errors.New("alarmd linkdmanage: the control plane URL must be an HTTP URL without credentials or query")
	}
	// No secret sends no Internal-Token header at all (the link answers
	// 401); a secret of only blanks is a mistake and refused.
	if (options.Secret != "" && strings.TrimSpace(options.Secret) == "") || strings.TrimSpace(options.Username) == "" ||
		options.Client == nil {
		return nil, errors.New("alarmd linkdmanage: the control plane username and client are required, and a secret is not blank")
	}
	// A copy that never follows a redirect: Go carries a custom header such
	// as Internal-Token across one, so a redirect would hand the token to
	// wherever it points. A 3xx is answered as the status it is.
	client := *options.Client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	maxBytes := options.MaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxPageBytes + 1<<20
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &Client{base: base, client: &client, secret: []byte(options.Secret), username: options.Username,
		maxBytes: maxBytes, now: now}, nil
}

// tokenLifetime is the token's life, the link's own (internaltoken.Lifetime).
const tokenLifetime = 5 * time.Minute

// tokenIssuedEarly is how far before now a token says it was issued. The
// link checks iat with no leeway (internaltoken: jwt.WithIssuedAt and no
// WithLeeway), so a token stamped this second is refused by a link whose
// clock is a fraction behind; a second earlier is within every clock the
// two share, and the token's life is unchanged.
const tokenIssuedEarly = time.Second

// token is a fresh Internal-Token value: one per request, so none is held
// past its life.
func (c *Client) token() string {
	encode := base64.RawURLEncoding.EncodeToString
	issued := c.now().Add(-tokenIssuedEarly).Unix()
	header := encode([]byte(`{"alg":"HS256","typ":"JWT"}`))
	claims, _ := json.Marshal(map[string]any{"username": c.username, "iat": issued, "exp": issued + int64(tokenLifetime/time.Second)})
	signing := header + "." + encode(claims)
	mac := hmac.New(sha256.New, c.secret)
	mac.Write([]byte(signing))
	return "Bearer " + signing + "." + encode(mac.Sum(nil))
}

// The routes.
const (
	RouteSearch      = "/api/v1/alerts/search"
	RouteCloseFilter = "/api/v1/alerts/close"
	routeCloseOne    = "/api/v1/alerts/%s/close"
)

// StatusError is a non-200 answer. Message is the link's text, cut; the
// link does not echo query values or connection details (alert-search.md).
type StatusError struct {
	Route   string
	Status  int
	Message string
}

func (e *StatusError) Error() string {
	return "alarmd linkdmanage: " + e.Route + " answered " + strconv.Itoa(e.Status) + ": " + e.Message
}

// Status is the link's status for err, 0 when the link gave none (the
// request failed, timed out, or the answer could not be read).
func Status(err error) int {
	var status *StatusError
	if errors.As(err, &status) {
		return status.Status
	}
	return 0
}

// Unsupported says the link cannot serve the search or the filtered close
// at all: its storage does not search (501), or it does not serve the
// route (404, 405 - a link older than the routes). Only for those two
// routes; a 404 of a single close is the alert's.
func Unsupported(err error) bool {
	var status *StatusError
	if !errors.As(err, &status) || status.Route == "close_one" {
		return false
	}
	switch status.Status {
	case http.StatusNotImplemented, http.StatusNotFound, http.StatusMethodNotAllowed:
		return true
	}
	return false
}

// post sends body and reads the answer into out on 200.
func (c *Client) post(ctx context.Context, route, path string, body []byte, out any) error {
	target := *c.base
	target.Path += path
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	if len(c.secret) > 0 {
		request.Header.Set("Internal-Token", c.token())
	}
	response, err := c.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, c.maxBytes+1))
	if err != nil {
		return err
	}
	if response.StatusCode != http.StatusOK {
		return &StatusError{Route: route, Status: response.StatusCode, Message: cut(strings.TrimSpace(string(payload)), 160)}
	}
	if int64(len(payload)) > c.maxBytes {
		return fmt.Errorf("alarmd linkdmanage: %s answered past %d bytes", route, c.maxBytes)
	}
	return json.Unmarshal(payload, out)
}

func cut(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	return text[:limit]
}

// Alert is what alarmd reads of one of the link's native alerts.
type Alert struct {
	AlertID       string                     `json:"alert_id"`
	TenantID      string                     `json:"bk_tenant_id"`
	EventSourceID string                     `json:"event_source_id"`
	Fingerprint   string                     `json:"fingerprint"`
	Status        string                     `json:"status"`
	Severity      string                     `json:"severity"`
	Labels        map[string]json.RawMessage `json:"labels"`
	Dimensions    map[string]json.RawMessage `json:"dimensions"`
	ExtraData     json.RawMessage            `json:"extra_data"`
	// CreateAt is when the link opened the alert, on the link's clock.
	CreateAt time.Time `json:"create_at"`
}

// Label reads a label the way the link's index does: a string as it is, an
// integer in plain decimal (labels are flattened keywords, so a number and
// its text match the same term). Anything else is no label.
func (alert Alert) Label(name string) (string, bool) {
	raw := alert.Labels[name]
	if len(raw) == 0 {
		return "", false
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text, text != ""
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var number json.Number
	if decoder.Decode(&number) != nil {
		return "", false
	}
	if _, err := strconv.ParseInt(number.String(), 10, 64); err != nil {
		return "", false
	}
	return number.String(), true
}

// SearchPage is one page of a search, in alert id order.
type SearchPage struct {
	Items []Alert `json:"items"`
	Next  string  `json:"next"`
}

// Search reads one page of the tenant's alerts matching query, an
// Elasticsearch query object.
func (c *Client) Search(ctx context.Context, tenantID string, query json.RawMessage, after string, limit int) (SearchPage, error) {
	if err := validRequest(query, limit); err != nil {
		return SearchPage{}, err
	}
	body, err := json.Marshal(struct {
		TenantID string          `json:"bk_tenant_id"`
		Query    json.RawMessage `json:"query"`
		After    string          `json:"after,omitempty"`
		Limit    int             `json:"limit"`
	}{tenantID, query, after, limit})
	if err != nil {
		return SearchPage{}, err
	}
	if len(body) > MaxFilterBodyBytes {
		return SearchPage{}, ErrBodyTooLarge
	}
	var page SearchPage
	if err := c.post(ctx, "search", RouteSearch, body, &page); err != nil {
		return SearchPage{}, err
	}
	return page, nil
}

// ErrInvalidRequest is a request the link would refuse with 400, not sent:
// no query or filter object (sent as JSON null), or a limit outside 1..500
// (b7e5beddf internal/controlplane/api/alert_search.go validAlertQuery,
// close_filter.go:68). A caller that means every alert says
// {"match_all":{}}.
var ErrInvalidRequest = errors.New("alarmd linkdmanage: a query object and a limit of 1..500 are required")

func validRequest(query json.RawMessage, limit int) error {
	trimmed := bytes.TrimSpace(query)
	if len(trimmed) == 0 || trimmed[0] != '{' || limit < 1 || limit > MaxLimit {
		return ErrInvalidRequest
	}
	return nil
}

// ErrBodyTooLarge is a request alarmd would not send: past the link's body
// bound, it would be refused whole (413).
var ErrBodyTooLarge = errors.New("alarmd linkdmanage: request body past the link's bound")

// Item is one alert's result in a filtered close: the single close's HTTP
// status, already_closed on a repeat of the same command, and the link's
// error code on a failure.
type Item struct {
	AlertID       string `json:"alert_id"`
	Status        int    `json:"status"`
	AlreadyClosed bool   `json:"already_closed,omitempty"`
	ErrorCode     string `json:"error_code,omitempty"`
}

// FilterPage is one filtered close request's answer.
type FilterPage struct {
	Items  []Item `json:"items"`
	Next   string `json:"next"`
	DryRun bool   `json:"dry_run"`
	Closed int    `json:"closed"`
	Failed int    `json:"failed"`
}

// CloseByFilter sends one filtered close: command is the close command's
// exact JSON object (Command.Bytes), sent byte for byte with the filter, the
// cursor, the limit and dry_run added before it; nil on a dry run.
func (c *Client) CloseByFilter(ctx context.Context, command []byte, tenantID string, filter json.RawMessage, after string, limit int,
	dryRun bool) (FilterPage, error) {
	if err := validRequest(filter, limit); err != nil {
		return FilterPage{}, err
	}
	head, err := json.Marshal(struct {
		TenantID string          `json:"bk_tenant_id,omitempty"`
		Filter   json.RawMessage `json:"filter"`
		After    string          `json:"after,omitempty"`
		Limit    int             `json:"limit"`
		DryRun   bool            `json:"dry_run"`
	}{TenantID: tenantIfBare(command, tenantID), Filter: filter, After: after, Limit: limit, DryRun: dryRun})
	if err != nil {
		return FilterPage{}, err
	}
	body := head
	if len(command) > 0 {
		trimmed := bytes.TrimSpace(command)
		if len(trimmed) < 2 || trimmed[0] != '{' || trimmed[len(trimmed)-1] != '}' {
			return FilterPage{}, errors.New("alarmd linkdmanage: the close command is not a JSON object")
		}
		// The command's own fields, unchanged, after the request's.
		body = append(append(head[:len(head)-1:len(head)-1], ','), trimmed[1:]...)
	}
	if len(body) > MaxFilterBodyBytes {
		return FilterPage{}, ErrBodyTooLarge
	}
	var page FilterPage
	if err := c.post(ctx, "close_filter", RouteCloseFilter, body, &page); err != nil {
		return FilterPage{}, err
	}
	return page, nil
}

// tenantIfBare is the tenant to write beside the filter: none when the
// command carries it (a real close), the tenant on a dry run without one.
func tenantIfBare(command []byte, tenantID string) string {
	if len(command) > 0 {
		return ""
	}
	return tenantID
}

// CloseOne closes one alert by its id with command, the exact bytes the
// filtered close sent. A non-200 is a *StatusError; already is the link's
// already_closed.
func (c *Client) CloseOne(ctx context.Context, alertID string, command []byte) (already bool, err error) {
	if alertID == "" {
		return false, errors.New("alarmd linkdmanage: an alert id is required")
	}
	if len(command) > MaxCloseBodyBytes {
		return false, ErrBodyTooLarge
	}
	var answer struct {
		AlreadyClosed bool `json:"already_closed"`
	}
	if err := c.post(ctx, "close_one", fmt.Sprintf(routeCloseOne, url.PathEscape(alertID)), command, &answer); err != nil {
		return false, err
	}
	return answer.AlreadyClosed, nil
}

// The 409 kinds of a single close. The management route answers them as
// text only (http.Error with the closer's message, b7e5beddf
// internal/controlplane/api/close.go:58-62), with no error code; these are
// the closer's messages (internal/lifecycle/process/close.go:144,264,267).
// A message none of them matches is ConflictUnknown.
const (
	ConflictInvalidTransition = "invalid_transition"
	ConflictVersion           = "version_conflict"
	ConflictSourceUnpublished = "source_unpublished"
	ConflictUnknown           = "conflict_unknown"
)

// ConflictKind names a single close's 409 by its message.
func ConflictKind(err error) string {
	var status *StatusError
	if !errors.As(err, &status) || status.Status != http.StatusConflict {
		return ""
	}
	switch {
	case strings.HasPrefix(status.Message, "alert state changed"):
		return ConflictInvalidTransition
	case strings.HasPrefix(status.Message, "alert version changed"):
		return ConflictVersion
	case strings.HasPrefix(status.Message, "event source has no published configuration"):
		return ConflictSourceUnpublished
	}
	return ConflictUnknown
}
