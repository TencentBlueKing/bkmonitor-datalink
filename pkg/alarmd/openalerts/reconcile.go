// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package openalerts

import (
	"bytes"
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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
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

// ErrNoDeadline refuses a Console call whose context has no deadline. The
// client has no timeout of its own -- each call is bounded by its caller,
// who knows what the call is for -- so a call without one could wait
// forever; it is refused before any request and is not a Console failure.
var ErrNoDeadline = errors.New("alarmd openalerts: a Console call needs a context deadline")

func requireDeadline(ctx context.Context) error {
	if _, bounded := ctx.Deadline(); !bounded {
		return ErrNoDeadline
	}
	return nil
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

// TargetCountError is a link that lists other than one target, with the
// targets' names: alarmd reads the sets of a link with exactly one.
type TargetCountError struct {
	Count int
	Names []string
}

func (err *TargetCountError) Error() string {
	return fmt.Sprintf("alarmd openalerts: the link lists %d targets, and alarmd reads the sets of a link with one (targets: %s)",
		err.Count, strings.Join(err.Names, ", "))
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
	BaseURL  string
	Username string
	Password string
	// Token is where the token comes from when there is no Basic pair: the
	// admin serves the link's API to the bearer of a token it issued and
	// handed over (linkdtoken). With a Basic pair it is not asked.
	Token            TokenSource
	Client           *http.Client
	Index            IndexLocation
	MaxResponseBytes int64
	// Now is the clock the call record is kept by; nil is the wall clock.
	Now func() time.Time
	// LocationConfirmed is whether Index was taken from the Console's own
	// target (its startup discovery adopted it). Every reconciliation then
	// confirms it again, or finds the link writing elsewhere.
	LocationConfirmed bool
	// OnLocationMismatch is told the target a reconciliation found writing
	// somewhere other than Index, outside the reader's lock, so the caller
	// can move its reads there.
	OnLocationMismatch func(TargetBinding)
	// KeyingEvery is how often a reconciliation reads the event source's
	// keying again; zero reads it only until it has answered once.
	KeyingEvery time.Duration
	// OnFailure is told of an operation's failure when a run of failures
	// starts and when its class changes, not on every failure while it
	// lasts, with the operation's failures so far; outside the record's
	// lock. Nil tells no one.
	OnFailure func(op, class string, failures uint64)
	// OutputTopic is the Kafka topic this deployment's events are written
	// to. The event source they arrive under is the target's candidate
	// subscribed to it (chooseSource); empty leaves the link's name.
	OutputTopic string
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
	// confirmed is ConsoleFacts.LocationConfirmed; keyingTried when the
	// event source was last asked for, answered or not; factsTried when
	// Refresh last resolved the target.
	confirmed               bool
	keyingTried, factsTried time.Time
	// calls is what this process has seen of each Console operation.
	calls consoleCalls
	// latency is the durations of this client's requests, which bound each
	// request a caller asks a coverage for (WithRequestCoverage).
	latency *ConsoleLatency
	// sourceChoice is how the last resolution chose the event source, and
	// chosenSource the source the last successful choice found.
	sourceChoice SourceByTopic
	chosenSource string
}

// bindingReuse is how long a resolved target is reused by the roster walk,
// which makes one request per page and would otherwise ask for the targets
// as often as it pages.
const bindingReuse = time.Minute

// TokenSource is the token a call to the link's API is made with, and where
// a refusal of it is told (linkdtoken.Store).
type TokenSource interface {
	Token(context.Context) (string, error)
	Rejected(context.Context, string) error
}

func NewHTTPReconciler(options HTTPReconcilerOptions) (*HTTPReconciler, error) {
	u, err := url.Parse(options.BaseURL)
	basic := options.Username != "" && options.Password != ""
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		options.Client == nil || options.MaxResponseBytes <= 0 || (!basic && options.Token == nil) ||
		(options.Username == "") != (options.Password == "") ||
		!validPrefix(options.Index.KeyPrefix) || options.Index.Address == "" || options.Index.Database < 0 {
		return nil, errors.New("alarmd openalerts: invalid reconciliation endpoint")
	}
	// Do not forward Basic Auth to another endpoint through a redirect.
	client := *options.Client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	options.Client = &client
	return &HTTPReconciler{options: options, confirmed: options.LocationConfirmed, latency: &ConsoleLatency{}}, nil
}

// Latency is how long this client's requests have taken, shared by every
// caller of the client.
func (reader *HTTPReconciler) Latency() *ConsoleLatency { return reader.latency }

func (reader *HTTPReconciler) get(ctx context.Context, path string, query url.Values, out any) error {
	return reader.getPath(ctx, "/strategy-index/"+path, query, out)
}

// getPath reads path under the link's API root, which BaseURL names whole:
// where the operations are served depends on what serves them - the admin
// module, or a Console of its own - and the deployment says which, not this
// process. Only the operations' own paths are appended.
func (reader *HTTPReconciler) getPath(ctx context.Context, path string, query url.Values, out any) error {
	return reader.getPathAnswering(ctx, path, query, out, false)
}

// getPathAnswering is getPath; with refusal, an answer other than 200 also
// carries the link's own words for it (LinkRefusal), for a read an operator
// asked for and has to see why the link turned down.
func (reader *HTTPReconciler) getPathAnswering(ctx context.Context, path string, query url.Values, out any, refusal bool) error {
	u := strings.TrimRight(reader.options.BaseURL, "/") + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return errors.New("alarmd openalerts: invalid reconcile request")
	}
	token := ""
	if reader.options.Username != "" {
		req.SetBasicAuth(reader.options.Username, reader.options.Password)
	} else {
		if token, err = reader.options.Token.Token(ctx); err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}
	// The request itself, once the token is in hand: bounded by its own
	// deadline when the caller named the coverage its work serves, and timed.
	// A request that got an HTTP answer (any status) or ran to its deadline
	// is a sample of the Console's latency; one that failed before either --
	// refused, unresolved, reset -- is counted apart (ConsoleLatency).
	requestCtx := ctx
	if coverage, ok := requestCoverage(ctx); ok {
		var cancel context.CancelFunc
		requestCtx, cancel = context.WithTimeout(ctx, reader.latency.Deadline(coverage))
		defer cancel()
		req = req.WithContext(requestCtx)
	}
	started, answered := time.Now(), false
	defer func() {
		switch {
		case answered || errors.Is(requestCtx.Err(), context.DeadlineExceeded):
			reader.latency.Observe(time.Since(started))
		default:
			reader.latency.NoteUnanswered()
		}
	}()
	response, err := reader.options.Client.Do(req)
	if err != nil {
		return transportError(execution.ClassifyTransportFailure(err))
	}
	answered = true
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized && token != "" {
		// The admin no longer accepts this token - its password or signing
		// key changed: say so, for it to hand over another. A failure to
		// record it leaves the call failing as it does anyway.
		_ = reader.options.Token.Rejected(ctx, token)
	}
	if response.StatusCode != http.StatusOK {
		if response.StatusCode == http.StatusNotFound && objectMissing(response.Body) {
			return errObjectMissing
		}
		if refusal {
			return linkRefusal(response.StatusCode, response.Body)
		}
		return statusError(response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, reader.options.MaxResponseBytes+1))
	if err != nil || int64(len(data)) > reader.options.MaxResponseBytes {
		return ErrIncomplete
	}
	if json.Unmarshal(data, out) != nil {
		// The page is told by its body, not its label: a JSON answer
		// labelled text/html would otherwise be refused as not_api.
		if trimmed := bytes.TrimSpace(data); len(trimmed) > 0 && trimmed[0] == '<' {
			return ErrNotAPI
		}
		return ErrIncomplete
	}
	return nil
}

// ErrNotAPI is a URL that answered with a page rather than the link's API:
// a Console's or the admin's web application serves its page at any path it
// has no route for, so a URL naming the server rather than its API root - the
// form the Console URL had before it named the root - reaches the page and
// would otherwise read as a garbled answer.
var ErrNotAPI = errors.New("alarmd openalerts: the Console URL answered with a web page, not the link's API; " +
	"it must name the API root (the admin module's .../api/modules/linkd, or a Console's .../local-api)")

// errObjectMissing is the link answering that the object a path names does
// not exist. Both servers answer a path they have no route for with the same
// status and error code, so the status alone cannot tell "no such alert" from
// "no such operation here" - a wrong API root would read as every alert gone.
// Only the object-missing answer's own message is taken as one; any other 404
// is a failure, so a changed message costs reads, never closes.
var errObjectMissing = errors.New("alarmd openalerts: the link has no such object")

// objectMissingMessage is the message both the admin module and a Console
// give when the object a path names does not exist (their route-missing
// answers carry the same code and a different message).
const objectMissingMessage = "对象不存在"

// objectMissing reports whether a 404's body is the link's object-missing
// answer: {"error": {"code": "not_found", "message": objectMissingMessage}}.
func objectMissing(body io.Reader) bool {
	var answer struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	data, err := io.ReadAll(io.LimitReader(body, 4096))
	if err != nil || json.Unmarshal(data, &answer) != nil {
		return false
	}
	return answer.Error.Code == "not_found" && answer.Error.Message == objectMissingMessage
}

// Binding is this deployment's target, read from the Console. It is reused
// for up to bindingReuse; so is Reconcile's, which checks it against the
// target the link answers for and reads it afresh when they differ.
func (reader *HTTPReconciler) Binding(ctx context.Context) (TargetBinding, error) {
	if err := requireDeadline(ctx); err != nil {
		return TargetBinding{}, err
	}
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
	binding = reader.chooseSource(ctx, binding)
	reader.mu.Lock()
	index := reader.options.Index
	reader.mu.Unlock()
	if binding.KeyPrefix != index.KeyPrefix || !strings.EqualFold(binding.Address, index.Address) || binding.Database != index.Database {
		reader.mu.Lock()
		reader.confirmed = false
		onMismatch := reader.options.OnLocationMismatch
		reader.mu.Unlock()
		if onMismatch != nil {
			onMismatch(binding)
		}
		return TargetBinding{}, fmt.Errorf("alarmd openalerts: the link writes open alert sets to %s db %d prefix %s, this process reads %s db %d prefix %s, and none of the Redis connections this process holds is at %s",
			binding.Address, binding.Database, binding.KeyPrefix, index.Address, index.Database, index.KeyPrefix, binding.Address)
	}
	reader.mu.Lock()
	reader.binding, reader.resolvedAt, reader.confirmed = binding, reader.now(), true
	reader.mu.Unlock()
	return binding, nil
}

// SetIndexLocation moves where this process says it reads the open alert
// sets from: to a place a later discovery found after a failed start, or to
// the one a reconciliation found the link writing to. The resolved target is
// forgotten, and the location is not confirmed until a resolution agrees
// with it - the next Refresh, at once, or the calibration the caller asks
// for - so nothing read before that is trusted as the link's word.
func (reader *HTTPReconciler) SetIndexLocation(index IndexLocation) error {
	if !validPrefix(index.KeyPrefix) || index.Address == "" || index.Database < 0 {
		return errors.New("alarmd openalerts: invalid index location")
	}
	reader.mu.Lock()
	defer reader.mu.Unlock()
	reader.options.Index = index
	reader.binding, reader.resolvedAt, reader.confirmed = TargetBinding{}, time.Time{}, false
	reader.keyingTried, reader.factsTried = time.Time{}, time.Time{}
	return nil
}

// choose is the one target the Console lists. A link that lists none, or
// several, is refused with the targets' names: the choice is never the first
// one that happened to come back, and a link that maintains several targets
// is the link's to disambiguate, not this deployment's.
func (reader *HTTPReconciler) choose(ctx context.Context) (TargetBinding, error) {
	var targets []TargetBinding
	if err := reader.get(ctx, "targets", nil, &targets); err != nil {
		return TargetBinding{}, err
	}
	if len(targets) > 512 {
		return TargetBinding{}, ErrIncomplete
	}
	names := make([]string, 0, len(targets))
	for _, target := range targets {
		names = append(names, target.EventSourceID+"/"+target.HookName)
	}
	if len(targets) != 1 {
		return TargetBinding{}, &TargetCountError{Count: len(targets), Names: names}
	}
	return normalizeBinding(targets[0])
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
	// The target is reused as Binding reuses it: reading the target list and
	// each source before every strategy made one reconciliation three
	// requests in sequence. The link names the target it answered for, so a
	// target that moved is still found -- the answer differs, the target is
	// read afresh (which moves where the sets are read when it must), and the
	// strategy is asked once more against it.
	reused := false
	reader.mu.Lock()
	b, at := reader.binding, reader.resolvedAt
	reader.mu.Unlock()
	if !at.IsZero() && reader.now().Sub(at) < bindingReuse {
		reused = true
	} else {
		var err error
		if b, err = reader.resolve(ctx); err != nil {
			return Reconciliation{}, err
		}
	}
	reader.refreshKeying(ctx, b)
	response, err := reader.askReconcile(ctx, b, key)
	if err == nil && reused && !sameTarget(b, response.Target) {
		if b, err = reader.resolve(ctx); err != nil {
			return Reconciliation{}, err
		}
		response, err = reader.askReconcile(ctx, b, key)
	}
	if err != nil {
		return Reconciliation{}, err
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

// reconcileResponse is the link's reconciliation of one strategy.
type reconcileResponse struct {
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

// askReconcile is one reconcile request for the strategy against b.
func (reader *HTTPReconciler) askReconcile(ctx context.Context, b TargetBinding, key StrategyKey) (reconcileResponse, error) {
	var response reconcileResponse
	query := url.Values{"event_source_id": {b.EventSourceID}, "hook_name": {b.HookName}, "bk_tenant_id": {key.TenantID}, "strategy_id": {key.StrategyID}}
	if err := reader.get(ctx, "reconcile", query, &response); err != nil {
		return reconcileResponse{}, err
	}
	if !response.Complete || !response.Redis.Complete || !response.Alerts.Complete || response.Rows == nil || len(response.Rows) > 10000 {
		return reconcileResponse{}, ErrIncomplete
	}
	return response, nil
}

// transportError is a Console request that got no answer, named by the
// transport word for why (execution.ClassifyTransportFailure). The text is
// this package's own and carries the word only, never the address the
// client's error would.
type transportError string

func (class transportError) Error() string {
	return "alarmd openalerts: Console request failed (" + string(class) + ")"
}

// statusError is a Console answer other than 200 that is not an answer about
// the object (errObjectMissing). A 404 here is a path the server has no route
// for, which says the Console URL is not the link's API root, and says so.
type statusError int

func (code statusError) Error() string {
	if int(code) == http.StatusNotFound {
		return "alarmd openalerts: Console HTTP status 404 for an operation's path; the Console URL must name the API root " +
			"(the admin module's .../api/modules/linkd, or a Console's .../local-api)"
	}
	return fmt.Sprintf("alarmd openalerts: Console HTTP status %d", int(code))
}

func containsSource(sources []string, value string) bool {
	index := sort.SearchStrings(sources, value)
	return index < len(sources) && sources[index] == value
}
