// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

// Package k8sread reads alarmd's own workload from the Kubernetes API with
// the Pod's ServiceAccount: its Pods' state and restarts, the events on its
// Deployment, ReplicaSets and Pods, and a bounded tail of a container's log.
//
// Only GET requests are sent, and only for the workload this process belongs
// to: the Deployment is found from this Pod's own owner chain and every read
// is scoped by that Deployment's selector. A Pod outside it is refused by
// name, whatever the ServiceAccount's role would allow.
//
// Every way a read can fail is a named Error. A read that did not happen is
// never returned as an empty list: "no events" is only ever said of a read
// that succeeded.
package k8sread

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// The in-cluster ServiceAccount files.
const (
	TokenPath     = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	CAPath        = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
	NamespacePath = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"
)

// Bounds. A read returns at most this much whatever the cluster holds.
const (
	MaxPods         = 32
	MaxReplicaSets  = 8
	MaxEvents       = 200
	MaxMessageBytes = 512
	DefaultLogLines = 200
	MaxLogLines     = 1000
	MaxLogBytes     = 256 << 10
	// A filtered read streams the log and keeps only the matching lines, so
	// what it scans is not bounded by what it returns. Without a window it
	// scans the last DefaultScanLines lines; ScanLines asks for another
	// tail, at most MaxScanLines. Either way the scan stops at
	// MaxScanBytes or scanMargin before the read's deadline, and says so.
	DefaultScanLines = 20000
	MaxScanLines     = 50000
	MaxScanBytes     = 512 << 20
	// MaxLogLineBytes bounds one matched line kept; a longer one is cut.
	MaxLogLineBytes = 8 << 10
	// scanMargin is left of the read's deadline to answer in.
	scanMargin = 500 * time.Millisecond
	// MaxLogFilters bounds the substrings one read filters on, and
	// MaxLogFilterBytes each of them.
	MaxLogFilters     = 4
	MaxLogFilterBytes = 128
	// MaxLogSinceSeconds bounds how far back a read may start.
	MaxLogSinceSeconds = 24 * 60 * 60
	// maxAPIBytes bounds one API response body before it is decoded.
	maxAPIBytes = 8 << 20
	// maxLogReadBytes bounds the whole tail read before its newest
	// MaxLogBytes are kept.
	maxLogReadBytes = 8 << 20
	// scopeTTL is how long the resolved Deployment is reused. A rollout
	// keeps the Deployment, so this only saves three GETs per read.
	scopeTTL = 5 * time.Minute
)

// Failure codes, closed.
const (
	// CodeNoServiceAccount: the ServiceAccount's token, CA or namespace
	// file is not mounted, so nothing can be asked.
	CodeNoServiceAccount = "service_account_not_mounted"
	// CodeNoAPIServer: KUBERNETES_SERVICE_HOST/PORT are not set.
	CodeNoAPIServer = "apiserver_not_configured"
	// CodeUnreachable: the API server did not answer (dial, TLS, timeout).
	CodeUnreachable = "apiserver_unreachable"
	// CodeUnauthorized: the API server rejected the token (401).
	CodeUnauthorized = "apiserver_unauthorized"
	// CodeForbidden: the ServiceAccount's role does not grant the read (403).
	CodeForbidden = "rbac_forbidden"
	// CodeNotFound: the object asked for does not exist (404), including a
	// previous container log that was never written.
	CodeNotFound = "not_found"
	// CodeScopeUnresolved: this Pod's owner chain does not lead to a
	// Deployment with a label selector this reader can use.
	CodeScopeUnresolved = "scope_unresolved"
	// CodeOutOfScope: the Pod or container asked for is not alarmd's.
	CodeOutOfScope = "not_in_scope"
	// CodeAPIError: any other refusal, with the server's status.
	CodeAPIError = "apiserver_error"
)

// Codes is every failure code, in the order a reader lists them.
var Codes = []string{CodeNoServiceAccount, CodeNoAPIServer, CodeUnreachable, CodeUnauthorized, CodeForbidden,
	CodeNotFound, CodeScopeUnresolved, CodeOutOfScope, CodeAPIError}

// Error is a named failure. Resource is what was being read when it failed.
type Error struct {
	Code     string
	Resource string
	Status   int
	Message  string
}

func (e *Error) Error() string {
	text := e.Code
	if e.Resource != "" {
		text += " reading " + e.Resource
	}
	if e.Status != 0 {
		text += fmt.Sprintf(" (HTTP %d)", e.Status)
	}
	if e.Message != "" {
		text += ": " + e.Message
	}
	return text
}

// Options configure a Reader. Zero paths take the in-cluster defaults, and a
// zero Host and Port are read from KUBERNETES_SERVICE_HOST/PORT.
type Options struct {
	// PodName is this process's own Pod, the start of the owner chain.
	PodName                          string
	Host, Port                       string
	TokenPath, CAPath, NamespacePath string
	// Client replaces the TLS client built from the CA; tests only.
	Client *http.Client
	Now    func() time.Time
}

// Reader answers the three reads. It holds no connection until asked.
type Reader struct {
	options Options

	mu      sync.Mutex
	client  *http.Client
	scope   Scope
	scopeAt time.Time
}

// Scope is the workload every read is limited to.
type Scope struct {
	Namespace  string `json:"namespace"`
	Deployment string `json:"deployment"`
	// Selector is the Deployment's label selector, as the API takes it.
	Selector string `json:"selector"`
	labels   map[string]string
}

// New returns a Reader. It never fails: a missing ServiceAccount or API
// server is reported by name on the first read, not at startup, so a
// deployment without them runs the same and says why it cannot answer.
func New(options Options) *Reader {
	if options.TokenPath == "" {
		options.TokenPath = TokenPath
	}
	if options.CAPath == "" {
		options.CAPath = CAPath
	}
	if options.NamespacePath == "" {
		options.NamespacePath = NamespacePath
	}
	if options.Host == "" && options.Port == "" {
		options.Host, options.Port = os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &Reader{options: options, client: options.Client}
}

func (r *Reader) httpClient() (*http.Client, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.client != nil {
		return r.client, nil
	}
	pem, err := os.ReadFile(r.options.CAPath)
	if err != nil {
		return nil, &Error{Code: CodeNoServiceAccount, Message: "cannot read the ServiceAccount CA certificate"}
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, &Error{Code: CodeNoServiceAccount, Message: "the ServiceAccount CA certificate holds no certificate"}
	}
	r.client = &http.Client{
		Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
			DialContext: (&net.Dialer{Timeout: 2 * time.Second}).DialContext, MaxIdleConnsPerHost: 2,
			IdleConnTimeout: 30 * time.Second, TLSHandshakeTimeout: 2 * time.Second},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return r.client, nil
}

// get sends one GET and returns the body, bounded by limit. A status of 400
// or above is a named Error carrying the server's own message.
func (r *Reader) get(ctx context.Context, resource, path string, query url.Values, limit int64) ([]byte, error) {
	body, err := r.open(ctx, resource, path, query)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	read, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, &Error{Code: CodeUnreachable, Resource: resource, Message: "the response was cut off"}
	}
	return read, nil
}

// open sends one GET and hands back the body unread, for a caller that
// reads it as it arrives. A status of 400 or above is a named Error
// carrying the server's own message, and the body is then closed.
func (r *Reader) open(ctx context.Context, resource, path string, query url.Values) (io.ReadCloser, error) {
	if r.options.Host == "" || r.options.Port == "" {
		return nil, &Error{Code: CodeNoAPIServer, Resource: resource, Message: "KUBERNETES_SERVICE_HOST/PORT are not set"}
	}
	token, err := os.ReadFile(r.options.TokenPath)
	if err != nil || len(strings.TrimSpace(string(token))) == 0 {
		return nil, &Error{Code: CodeNoServiceAccount, Resource: resource, Message: "no ServiceAccount token is mounted"}
	}
	client, err := r.httpClient()
	if err != nil {
		var named *Error
		if errors.As(err, &named) {
			named.Resource = resource
		}
		return nil, err
	}
	target := url.URL{Scheme: "https", Host: net.JoinHostPort(r.options.Host, r.options.Port), Path: path, RawQuery: query.Encode()}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, &Error{Code: CodeAPIError, Resource: resource, Message: "cannot build the request"}
	}
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	request.Header.Set("Accept", "application/json, */*")
	response, err := client.Do(request)
	if err != nil {
		return nil, &Error{Code: CodeUnreachable, Resource: resource, Message: unreachableText(err)}
	}
	if response.StatusCode >= 400 {
		defer response.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(response.Body, maxAPIBytes))
		return nil, &Error{Code: codeOf(response.StatusCode), Resource: resource, Status: response.StatusCode, Message: statusMessage(body)}
	}
	return response.Body, nil
}

func unreachableText(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "no answer before the deadline"
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "no answer before the deadline"
	}
	var certErr *tls.CertificateVerificationError
	if errors.As(err, &certErr) {
		return "the API server's certificate does not verify against the ServiceAccount CA"
	}
	return "no connection to the API server"
}

func codeOf(status int) string {
	switch status {
	case http.StatusUnauthorized:
		return CodeUnauthorized
	case http.StatusForbidden:
		return CodeForbidden
	case http.StatusNotFound:
		return CodeNotFound
	}
	return CodeAPIError
}

// statusMessage is the API server's own sentence from a Status body, bounded.
func statusMessage(body []byte) string {
	var status struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &status) == nil && status.Message != "" {
		return bounded(status.Message)
	}
	return ""
}

func bounded(text string) string {
	if len(text) <= MaxMessageBytes {
		return text
	}
	cut := MaxMessageBytes
	for cut > 0 && (text[cut]&0xC0) == 0x80 {
		cut--
	}
	return text[:cut] + "…"
}

func (r *Reader) getJSON(ctx context.Context, resource, path string, query url.Values, into any) error {
	body, err := r.get(ctx, resource, path, query, maxAPIBytes)
	if err != nil {
		return err
	}
	if int64(len(body)) > maxAPIBytes {
		return &Error{Code: CodeAPIError, Resource: resource, Message: "the response exceeds the read bound"}
	}
	if err := json.Unmarshal(body, into); err != nil {
		return &Error{Code: CodeAPIError, Resource: resource, Message: "the response is not the expected object"}
	}
	return nil
}

type objectMeta struct {
	Name              string            `json:"name"`
	Labels            map[string]string `json:"labels"`
	CreationTimestamp time.Time         `json:"creationTimestamp"`
	DeletionTimestamp *time.Time        `json:"deletionTimestamp"`
	OwnerReferences   []struct {
		Kind       string `json:"kind"`
		Name       string `json:"name"`
		Controller *bool  `json:"controller"`
	} `json:"ownerReferences"`
}

func (m objectMeta) controller(kind string) string {
	for _, owner := range m.OwnerReferences {
		if owner.Kind == kind && owner.Controller != nil && *owner.Controller {
			return owner.Name
		}
	}
	return ""
}

// Resolve finds the Deployment this Pod belongs to: Pod, its controlling
// ReplicaSet, that ReplicaSet's controlling Deployment, and the Deployment's
// matchLabels. The answer is reused for scopeTTL.
func (r *Reader) Resolve(ctx context.Context) (Scope, error) {
	r.mu.Lock()
	if r.scope.Deployment != "" && r.options.Now().Sub(r.scopeAt) < scopeTTL {
		scope := r.scope
		r.mu.Unlock()
		return scope, nil
	}
	r.mu.Unlock()
	raw, err := os.ReadFile(r.options.NamespacePath)
	namespace := strings.TrimSpace(string(raw))
	if err != nil || namespace == "" {
		return Scope{}, &Error{Code: CodeNoServiceAccount, Resource: "namespace", Message: "no ServiceAccount namespace is mounted"}
	}
	if r.options.PodName == "" {
		return Scope{}, &Error{Code: CodeScopeUnresolved, Resource: "pod", Message: "this process does not know its own Pod name"}
	}
	base := "/api/v1/namespaces/" + url.PathEscape(namespace)
	apps := "/apis/apps/v1/namespaces/" + url.PathEscape(namespace)
	var pod struct {
		Metadata objectMeta `json:"metadata"`
	}
	if err := r.getJSON(ctx, "pods/"+r.options.PodName, base+"/pods/"+url.PathEscape(r.options.PodName), nil, &pod); err != nil {
		return Scope{}, err
	}
	replicaSet := pod.Metadata.controller("ReplicaSet")
	if replicaSet == "" {
		return Scope{}, &Error{Code: CodeScopeUnresolved, Resource: "pods/" + r.options.PodName, Message: "this Pod is not controlled by a ReplicaSet"}
	}
	var rs struct {
		Metadata objectMeta `json:"metadata"`
	}
	if err := r.getJSON(ctx, "replicasets/"+replicaSet, apps+"/replicasets/"+url.PathEscape(replicaSet), nil, &rs); err != nil {
		return Scope{}, err
	}
	deployment := rs.Metadata.controller("Deployment")
	if deployment == "" {
		return Scope{}, &Error{Code: CodeScopeUnresolved, Resource: "replicasets/" + replicaSet, Message: "the ReplicaSet is not controlled by a Deployment"}
	}
	var dep deploymentObject
	if err := r.getJSON(ctx, "deployments/"+deployment, apps+"/deployments/"+url.PathEscape(deployment), nil, &dep); err != nil {
		return Scope{}, err
	}
	if len(dep.Spec.Selector.MatchExpressions) > 0 || len(dep.Spec.Selector.MatchLabels) == 0 {
		return Scope{}, &Error{Code: CodeScopeUnresolved, Resource: "deployments/" + deployment, Message: "the Deployment's selector is not a plain label match"}
	}
	scope := Scope{Namespace: namespace, Deployment: deployment, Selector: selectorOf(dep.Spec.Selector.MatchLabels), labels: dep.Spec.Selector.MatchLabels}
	r.mu.Lock()
	r.scope, r.scopeAt = scope, r.options.Now()
	r.mu.Unlock()
	return scope, nil
}

func selectorOf(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+labels[key])
	}
	return strings.Join(parts, ",")
}

func (s Scope) matches(labels map[string]string) bool {
	for key, value := range s.labels {
		if labels[key] != value {
			return false
		}
	}
	return len(s.labels) > 0
}
