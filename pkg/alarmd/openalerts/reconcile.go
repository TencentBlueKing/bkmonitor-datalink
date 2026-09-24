// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package openalerts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"
)

// Reconciliation is a complete alert-store observation. Missing and Suppressed
// are the index differences to preserve across ordinary SET reads.
type Reconciliation struct {
	// EventSourceID is the source of the target that answered: this
	// deployment's own, which is how an alert's producer is told apart.
	EventSourceID string
	Members       []string
	Missing       []string
	Suppressed    []string
	Alerts        []Alert
}

// Severity is optional in older Console responses. Its absence never prevents
// membership recovery, but cannot be used as a fabricated close severity.
type Alert struct {
	AlertID       string `json:"alertId"`
	EventSourceID string `json:"eventSourceId"`
	Fingerprint   string `json:"fingerprint"`
	Severity      string `json:"severity,omitempty"`
}

type Reconciler interface {
	Reconcile(context.Context, StrategyKey) (Reconciliation, error)
}

// TargetBinding is one of the alert link's targets as its Console lists it:
// which source and hook it belongs to, and where its open alert sets are.
type TargetBinding struct {
	EventSourceID string   `json:"eventSourceId"`
	HookName      string   `json:"hookName"`
	KeyPrefix     string   `json:"keyPrefix"`
	Address       string   `json:"address"`
	Database      int      `json:"database"`
	Sources       []string `json:"sources"`
}

// TargetSelector picks which of the link's targets is this deployment's. Both
// fields are optional: with neither, the one target the link lists is used,
// and a link that lists several is refused with their names, so the choice is
// never the first one that happened to come back.
type TargetSelector struct {
	EventSourceID string
	HookName      string
}

// IndexLocation is where this process reads the open alert sets from. The
// target the link writes them to must be the same place, or every set this
// process reads is empty whatever the link holds.
type IndexLocation struct {
	KeyPrefix string
	Address   string
	Database  int
}

type HTTPReconcilerOptions struct {
	BaseURL          string
	Username         string
	Password         string
	Client           *http.Client
	Select           TargetSelector
	Index            IndexLocation
	MaxResponseBytes int64
	// Now is the clock the call record is kept by; nil is the wall clock.
	Now func() time.Time
}

// HTTPReconciler reads the link's Console. Which target is this deployment's
// is not configured but read from the Console itself - the link publishes
// the source, hook, prefix and location of every target it maintains - and
// it is read again on every reconciliation and at most a minute apart on the
// roster, so a target the link changes is noticed rather than assumed.
type HTTPReconciler struct {
	options    HTTPReconcilerOptions
	mu         sync.Mutex
	binding    TargetBinding
	resolvedAt time.Time
	// calls is what this process has seen of each Console operation.
	calls consoleCalls
}

// bindingReuse is how long a resolved target is reused by the roster walk,
// which makes one request per page and would otherwise ask for the targets
// as often as it pages.
const bindingReuse = time.Minute

func NewHTTPReconciler(options HTTPReconcilerOptions) (*HTTPReconciler, error) {
	u, err := url.Parse(options.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		options.Client == nil || options.MaxResponseBytes <= 0 || options.Username == "" || options.Password == "" ||
		!validPrefix(options.Index.KeyPrefix) || options.Index.Address == "" || options.Index.Database < 0 {
		return nil, errors.New("alarmd openalerts: invalid reconciliation endpoint")
	}
	// Do not forward Basic Auth to another endpoint through a redirect.
	client := *options.Client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	options.Client = &client
	return &HTTPReconciler{options: options}, nil
}

func (reader *HTTPReconciler) get(ctx context.Context, path string, query url.Values, out any) error {
	return reader.getPath(ctx, "/local-api/strategy-index/"+path, query, out)
}

func (reader *HTTPReconciler) getPath(ctx context.Context, path string, query url.Values, out any) error {
	u := strings.TrimRight(reader.options.BaseURL, "/") + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return errors.New("alarmd openalerts: invalid reconcile request")
	}
	req.SetBasicAuth(reader.options.Username, reader.options.Password)
	response, err := reader.options.Client.Do(req)
	if err != nil {
		return errors.New("alarmd openalerts: Console request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return statusError(response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, reader.options.MaxResponseBytes+1))
	if err != nil || int64(len(data)) > reader.options.MaxResponseBytes {
		return ErrIncomplete
	}
	if json.Unmarshal(data, out) != nil {
		return ErrIncomplete
	}
	return nil
}

// Binding is this deployment's target, read from the Console. It is reused
// for up to bindingReuse; Reconcile always reads it afresh.
func (reader *HTTPReconciler) Binding(ctx context.Context) (TargetBinding, error) {
	reader.mu.Lock()
	binding, at := reader.binding, reader.resolvedAt
	reader.mu.Unlock()
	if !at.IsZero() && reader.now().Sub(at) < bindingReuse {
		return binding, nil
	}
	return reader.resolve(ctx)
}

// DiscoverTarget reads which of the link's targets is this deployment's,
// without asking whether this process reads its sets from where the target
// writes them. It is how a deployment that states only the Console learns
// where to read: the answer is the link's, not a second copy of it kept in
// this process's configuration.
func DiscoverTarget(ctx context.Context, options HTTPReconcilerOptions) (TargetBinding, error) {
	options.Index = IndexLocation{KeyPrefix: "alarmd:open_alerts", Address: "discovery"}
	reader, err := NewHTTPReconciler(options)
	if err != nil {
		return TargetBinding{}, err
	}
	return reader.choose(ctx)
}

func (reader *HTTPReconciler) resolve(ctx context.Context) (TargetBinding, error) {
	binding, err := reader.choose(ctx)
	if err != nil {
		return TargetBinding{}, err
	}
	reader.mu.Lock()
	index := reader.options.Index
	reader.mu.Unlock()
	if binding.KeyPrefix != index.KeyPrefix || !strings.EqualFold(binding.Address, index.Address) || binding.Database != index.Database {
		return TargetBinding{}, fmt.Errorf("alarmd openalerts: the link writes open alert sets to %s db %d prefix %s, this process reads %s db %d prefix %s, and none of the Redis connections this process holds is at %s",
			binding.Address, binding.Database, binding.KeyPrefix, index.Address, index.Database, index.KeyPrefix, binding.Address)
	}
	reader.mu.Lock()
	reader.binding, reader.resolvedAt = binding, reader.now()
	reader.mu.Unlock()
	return binding, nil
}

// SetIndexLocation moves where this process says it reads the open alert
// sets from. A process that could not ask the Console at startup reads from
// a fallback location, and every reconciliation refuses with both locations
// named; once a later discovery finds where the link writes, the reader is
// rebound there and this is the reconciler's half of that move. The resolved
// target is forgotten so the next reconciliation checks the new location.
func (reader *HTTPReconciler) SetIndexLocation(index IndexLocation) error {
	if !validPrefix(index.KeyPrefix) || index.Address == "" || index.Database < 0 {
		return errors.New("alarmd openalerts: invalid index location")
	}
	reader.mu.Lock()
	defer reader.mu.Unlock()
	reader.options.Index = index
	reader.binding, reader.resolvedAt = TargetBinding{}, time.Time{}
	return nil
}

// choose is the target the selector picks among those the Console lists.
func (reader *HTTPReconciler) choose(ctx context.Context) (TargetBinding, error) {
	var targets []TargetBinding
	if err := reader.get(ctx, "targets", nil, &targets); err != nil {
		return TargetBinding{}, err
	}
	if len(targets) > 512 {
		return TargetBinding{}, ErrIncomplete
	}
	selector := reader.options.Select
	candidates := make([]TargetBinding, 0, 1)
	names := make([]string, 0, len(targets))
	for _, target := range targets {
		names = append(names, target.EventSourceID+"/"+target.HookName)
		if (selector.EventSourceID == "" || target.EventSourceID == selector.EventSourceID) &&
			(selector.HookName == "" || target.HookName == selector.HookName) {
			candidates = append(candidates, target)
		}
	}
	switch {
	case len(candidates) == 0:
		return TargetBinding{}, fmt.Errorf("alarmd openalerts: the link lists no target matching event_source_id=%q hook_name=%q (targets: %s)",
			selector.EventSourceID, selector.HookName, strings.Join(names, ", "))
	case len(candidates) > 1:
		return TargetBinding{}, fmt.Errorf("alarmd openalerts: the link lists %d targets, set linkd event_source_id and hook_name to choose one (targets: %s)",
			len(candidates), strings.Join(names, ", "))
	}
	return normalizeBinding(candidates[0])
}

func normalizeBinding(b TargetBinding) (TargetBinding, error) {
	if b.EventSourceID == "" || b.HookName == "" || !validPrefix(b.KeyPrefix) || b.Address == "" || b.Database < 0 ||
		len(b.Sources) == 0 || len(b.Sources) > 64 {
		return TargetBinding{}, errors.New("alarmd openalerts: the link listed an invalid target")
	}
	b.Sources = append([]string(nil), b.Sources...)
	sort.Strings(b.Sources)
	found := false
	for i, source := range b.Sources {
		if source == "" || (i > 0 && source == b.Sources[i-1]) {
			return TargetBinding{}, errors.New("alarmd openalerts: invalid source scope")
		}
		found = found || source == b.EventSourceID
	}
	if !found {
		return TargetBinding{}, errors.New("alarmd openalerts: binding source is outside allowed scope")
	}
	return b, nil
}

// sameTarget says a response was produced for the target this call resolved.
func sameTarget(binding, target TargetBinding) bool {
	target.Sources = append([]string(nil), target.Sources...)
	sort.Strings(target.Sources)
	return reflect.DeepEqual(binding, target)
}

func (reader *HTTPReconciler) reconcile(ctx context.Context, key StrategyKey) (Reconciliation, error) {
	if !validStrategyKey(key) {
		return Reconciliation{}, errors.New("alarmd openalerts: invalid strategy identity")
	}
	b, err := reader.resolve(ctx)
	if err != nil {
		return Reconciliation{}, err
	}
	var response struct {
		Target     TargetBinding `json:"target"`
		TenantID   string        `json:"tenantId"`
		StrategyID string        `json:"strategyId"`
		Key        string        `json:"key"`
		Complete   bool          `json:"complete"`
		Redis      struct {
			Complete bool `json:"complete"`
		} `json:"redis"`
		Alerts struct {
			Complete bool `json:"complete"`
		} `json:"alerts"`
		Rows []struct {
			Fingerprint string  `json:"fingerprint"`
			Status      string  `json:"status"`
			Alerts      []Alert `json:"alerts"`
		} `json:"rows"`
	}
	query := url.Values{"event_source_id": {b.EventSourceID}, "hook_name": {b.HookName}, "bk_tenant_id": {key.TenantID}, "strategy_id": {key.StrategyID}}
	if err := reader.get(ctx, "reconcile", query, &response); err != nil {
		return Reconciliation{}, err
	}
	if !response.Complete || !response.Redis.Complete || !response.Alerts.Complete || response.Rows == nil || len(response.Rows) > 10000 {
		return Reconciliation{}, ErrIncomplete
	}
	if !sameTarget(b, response.Target) || response.TenantID != key.TenantID || response.StrategyID != key.StrategyID || response.Key != b.KeyPrefix+":"+key.TenantID+":"+key.StrategyID {
		return Reconciliation{}, errors.New("alarmd openalerts: reconciliation identity mismatch")
	}
	result := Reconciliation{EventSourceID: b.EventSourceID}
	seen := make(map[string]struct{}, len(response.Rows))
	activeCount := 0
	for _, row := range response.Rows {
		if row.Fingerprint == "" || len(row.Fingerprint) > 4096 || row.Alerts == nil {
			return Reconciliation{}, ErrIncomplete
		}
		if _, exists := seen[row.Fingerprint]; exists {
			return Reconciliation{}, ErrIncomplete
		}
		seen[row.Fingerprint] = struct{}{}
		for _, alert := range row.Alerts {
			activeCount++
			if activeCount > 5000 || alert.AlertID == "" || alert.Fingerprint != row.Fingerprint || !containsSource(b.Sources, alert.EventSourceID) {
				return Reconciliation{}, ErrIncomplete
			}
		}
		switch row.Status {
		case "matched", "missing_redis":
			if len(row.Alerts) == 0 {
				return Reconciliation{}, ErrIncomplete
			}
			result.Members = append(result.Members, row.Fingerprint)
			result.Alerts = append(result.Alerts, row.Alerts...)
			if row.Status == "missing_redis" {
				result.Missing = append(result.Missing, row.Fingerprint)
			}
		case "redis_only":
			if len(row.Alerts) != 0 {
				return Reconciliation{}, ErrIncomplete
			}
			result.Suppressed = append(result.Suppressed, row.Fingerprint)
		default:
			return Reconciliation{}, ErrIncomplete
		}
	}
	return result, nil
}

// statusError is a Console answer other than 200. It keeps the code so a
// caller for which one code is an answer rather than a failure - the alert
// record's 404 - can tell it apart.
type statusError int

func (code statusError) Error() string {
	return fmt.Sprintf("alarmd openalerts: Console HTTP status %d", int(code))
}

func containsSource(sources []string, value string) bool {
	index := sort.SearchStrings(sources, value)
	return index < len(sources) && sources[index] == value
}
