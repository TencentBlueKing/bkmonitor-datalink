// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

// Package pod provides Pod evidence, bounded non-TTY execution and a few
// workload management actions in any namespace the ServiceAccount may reach.
// It does not authenticate callers; the channel admits them. A request names
// one namespace (this process's own by default) and a label selector; a
// target is pinned by Pod UID, container ID and image ID. Existing Worker
// k8sread operations are separate.
package pod

import (
	"context"
	"io"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// TimeoutContract is the remote supervisor's: the target's /bin/sh runs
	// the command in its own process group - under setsid where the image has
	// it, else as a job under the shell's job control, which dash and busybox
	// ash turn off without a tty - and a sleep and kill timer ends it at its
	// deadline; whether a group exists is checked, not assumed. Where the target has
	// no /bin/sh, or no sleep, the command runs unsupervised and the receipt
	// says so.
	TimeoutContract = "posix_sh_group_v1"
	// ReleaseLabel is the label a target's release is read from.
	ReleaseLabel = "app.kubernetes.io/instance"
	// DefaultModuleLabel is the label a target's module is read from when
	// the deployment names no other (Options.ModuleLabel).
	DefaultModuleLabel = "app.kubernetes.io/component"
	// StartCommandUnavailable is a command the target's runtime refused to
	// start, in a container run without a shell.
	StartCommandUnavailable = "command_unavailable"
	// How a run was supervised: the command led a process group of its own
	// (it is ended with its descendants), it did not - the shell had no job
	// control and the image no setsid, so only the command itself is ended
	// and its descendants may stay - or not at all.
	SupervisionShGroup = "sh_group"
	SupervisionShPID   = "sh_pid"
	SupervisionNone    = "none"
	// Why a run was not supervised.
	UnsupervisedNoShell = "no_shell"
	UnsupervisedNoSleep = "no_sleep"
	// What the supervisor's own stderr said when it failed before its first
	// record, by category only.
	BootstrapSilent     = "silent"
	BootstrapNotFound   = "not_found"
	BootstrapPermission = "permission"
	BootstrapSyntax     = "syntax"
	BootstrapOther      = "other"
	VerificationTimeout = 3 * time.Second
	TerminationGrace    = 4 * time.Second
	// StartupAllowance bounds connection setup and the supervisor's start
	// before its started report. With VerificationTimeout before and after
	// and TerminationGrace after the command, it is what the operation
	// ceiling keeps back from the command (MaxExecTimeout).
	StartupAllowance = 4 * time.Second
	RemoteNotStarted = "remote_not_started"
	RemoteCompleted  = "remote_completed"
	RemoteTimedOut   = "remote_timeout_confirmed"
	RemoteUnknown    = "remote_state_unknown"
	// RemoteUnsupervised is a run nothing in the target could end: its exit
	// code when it ended inside the deadline, StreamCut when it did not.
	RemoteUnsupervised     = "remote_unsupervised"
	CodeInvalidRequest     = "invalid_request"
	CodeNotInScope         = "not_in_scope"
	CodeTargetChanged      = "target_changed"
	CodeTargetNotRunning   = "target_not_running"
	CodeTimeoutUnsupported = "remote_timeout_unsupported"
	CodeCommandStartFailed = "command_start_failed"
	CodeAPIError           = "apiserver_error"
	CodeForbidden          = "rbac_forbidden"
	CodeNotFound           = "not_found"
	// CodeKindNotInstalled is a custom resource kind whose CRD the cluster
	// does not have.
	CodeKindNotInstalled = "kind_not_installed"
	CodeCanceled         = "request_canceled"
)

// Error deliberately omits raw API/transport errors, which can contain URLs,
// authorization material or supplied scripts. Code is the public contract.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

type Workload struct {
	Kind string `json:"kind" yaml:"kind"`
	Name string `json:"name" yaml:"name"`
	UID  string `json:"uid,omitempty" yaml:"uid,omitempty"`
}

// Client exposes only the Kubernetes reads needed for evidence and owner checks.
type Client interface {
	ListPods(context.Context, string, metav1.ListOptions) (*corev1.PodList, error)
	GetPod(context.Context, string, string) (*corev1.Pod, error)
	GetOwner(context.Context, string, metav1.OwnerReference) (OwnerResource, error)
	OpenLogs(context.Context, string, string, corev1.PodLogOptions) (io.ReadCloser, error)
}

type OwnerResource struct {
	Kind, Namespace, Name, UID string
	Labels                     map[string]string
	Owners                     []metav1.OwnerReference
	Selector                   *metav1.LabelSelector
	// Replicas is spec.replicas, nil where the kind has none.
	Replicas *int32
}

// Manager is the Kubernetes writes the management actions make. Every write
// carries the identity it was decided on: a Pod deletion its UID as a
// precondition, a workload patch its UID in metadata, so a replaced object
// is refused by the apiserver rather than changed.
type Manager interface {
	DeletePod(ctx context.Context, namespace, name, uid string) error
	GetWorkload(ctx context.Context, namespace, kind, name string) (OwnerResource, error)
	PatchWorkload(ctx context.Context, namespace, kind, name string, patch []byte) error
}

// Executor transports one exec. It must honor context cancellation, never
// retry, and never allocate a TTY. A known process exit is distinct from a
// transport error; neither cancellation nor transport failure proves termination.
type Executor interface {
	Stream(context.Context, StreamRequest) (StreamResult, error)
}
type StreamRequest struct {
	Target         Target
	Argv           []string
	Stdin          io.Reader
	Stdout, Stderr io.Writer
}
type StreamResult struct {
	ExitCode *int
	// StartRefused is the runtime refusing to start argv[0] (not found, not
	// executable): nothing ran.
	StartRefused bool
}

type Options struct {
	Client   Client
	Manager  Manager
	Executor Executor
	// Secrets reads the Secrets the deployment's whitelist names; nil
	// leaves secret.get unavailable.
	Secrets SecretReader
	// ConfigMaps reads ConfigMaps; nil leaves configmap.get unavailable.
	ConfigMaps ConfigMapReader
	// Resources reads the kinds k8s.list serves; nil leaves it unavailable.
	Resources ResourceReader
	// DryRun is k8s.dry_run's Kubernetes access; nil leaves it unavailable.
	DryRun DryRunner
	// Writer is k8s.apply's and k8s.delete's Kubernetes write; nil leaves
	// them unavailable.
	Writer Writer
	// DefaultNamespace is the namespace a request that names none reads:
	// this process's own. Empty means a request must name one.
	DefaultNamespace string
	// ModuleLabel is the label key a target's module is read from and a
	// discovery's module filter matches; DefaultModuleLabel when empty.
	ModuleLabel                   string
	DefaultTimeout, MaxTimeout    time.Duration
	MaxOutputBytes, MaxStdinBytes int64
	MaxPods                       int
	// LiteralSecrets supplements the documented credential_fields_v1 redactor.
	// Secrets stay in memory and are never included in an observation or receipt.
	LiteralSecrets []string
	// FingerprintKey keys the fingerprint every Secret value, and every
	// value masked under a credential name, is shown as: HMAC-SHA256 under
	// a key local to the deployment, so a fingerprint compares values within
	// it and cannot be checked against a table of common values made
	// elsewhere. Never output. Empty, the process makes its own and
	// FingerprintScope reads "process".
	FingerprintKey []byte
	// FingerprintScope is where fingerprints compare: "deployment" for a
	// key every replica shares, "process" for one this process made.
	FingerprintScope string
	// FingerprintKeySource, when FingerprintKey is empty, is asked once, at
	// the first fingerprint, for the deployment's key: building the provider
	// does no I/O. Nil from it, the process makes its own key.
	FingerprintKeySource func() []byte
	Now                  func() time.Time
	// startupAllowance replaces StartupAllowance when set; only tests about
	// what a run reports, not how long its start may take, set it.
	startupAllowance time.Duration
}

// Target is the identity a caller received from Discover or Inspect. All six
// fields are required for Logs and Exec; Inspect may initially omit identities.
type Target struct {
	Namespace   string `json:"namespace"`
	Pod         string `json:"pod"`
	UID         string `json:"uid"`
	Container   string `json:"container"`
	ContainerID string `json:"container_id"`
	ImageID     string `json:"image_id"`
}
type Observation struct {
	Target Target `json:"target"`
	// Init says the container is an init container of the Pod; its
	// identity and state are its init-container status.
	Init               bool         `json:"init,omitempty"`
	Release            string       `json:"release,omitempty"`
	Module             string       `json:"module,omitempty"`
	Workload           Workload     `json:"workload"`
	OwnerChain         []Workload   `json:"owner_chain"`
	Image              string       `json:"image"`
	Phase              string       `json:"phase"`
	Ready              bool         `json:"ready"`
	Running            bool         `json:"running"`
	RestartCount       int32        `json:"restart_count"`
	WaitingReason      string       `json:"waiting_reason,omitempty"`
	CurrentTermination *Termination `json:"current_termination,omitempty"`
	LastTermination    *Termination `json:"last_termination,omitempty"`
	Conditions         []Condition  `json:"conditions"`
	DeletionTimestamp  *time.Time   `json:"deletion_timestamp,omitempty"`
	NodeName           string       `json:"node_name,omitempty"`
	ObservedAt         time.Time    `json:"observed_at"`
}
type Termination struct {
	Reason     string    `json:"reason"`
	ExitCode   int32     `json:"exit_code"`
	Signal     int32     `json:"signal"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
}
type Condition struct {
	Type   string `json:"type"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}
type Limits struct {
	DefaultTimeoutMS int64 `json:"default_timeout_ms"`
	MaxTimeoutMS     int64 `json:"max_timeout_ms"`
	MaxOutputBytes   int64 `json:"max_output_bytes"`
	MaxStdinBytes    int64 `json:"max_stdin_bytes"`
	MaxPods          int   `json:"max_pods"`
}
type DiscoverRequest struct {
	Namespace string `json:"namespace"`
	// Selector is a Kubernetes label selector, handed to the apiserver.
	Selector string `json:"selector,omitempty"`
	Release  string `json:"release,omitempty"`
	Module   string `json:"module,omitempty"`
	Workload string `json:"workload,omitempty"`
	Continue string `json:"continue,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}
type DiscoverReceipt struct {
	Targets    []Observation `json:"targets"`
	Continue   string        `json:"continue,omitempty"`
	Rejected   int           `json:"rejected"`
	ObservedAt time.Time     `json:"observed_at"`
}
type TargetRequest struct {
	Target Target `json:"target"`
}
type LogsRequest struct {
	Target       Target `json:"target"`
	TailLines    int64  `json:"tail_lines,omitempty"`
	SinceSeconds int64  `json:"since_seconds,omitempty"`
	Previous     bool   `json:"previous,omitempty"`
	OutputBytes  int64  `json:"output_bytes,omitempty"`
	// Contains keeps only the lines holding it, and SinceTime and UntilTime
	// only those written in that span; the lines are scanned here, as they
	// stream, and only the kept ones are held.
	Contains  string    `json:"contains,omitempty"`
	SinceTime time.Time `json:"since_time,omitempty"`
	UntilTime time.Time `json:"until_time,omitempty"`
}
type LogsReceipt struct {
	Target            Target       `json:"target"`
	Previous          bool         `json:"previous"`
	Output            string       `json:"output"`
	Truncated         bool         `json:"truncated"`
	ObservedAfter     *Observation `json:"observed_after,omitempty"`
	TargetChanged     bool         `json:"target_changed"`
	EvidenceScope     string       `json:"evidence_scope"`
	RedactionContract string       `json:"redaction_contract"`
	ObservedAt        time.Time    `json:"observed_at"`
	// ScannedLines and MatchedLines are what a filtered read went through
	// and kept; absent on an unfiltered read.
	ScannedLines int64 `json:"scanned_lines,omitempty"`
	MatchedLines int64 `json:"matched_lines,omitempty"`
}
type ExecRequest struct {
	// RequestID is the channel request the call came in on, so the receipt
	// and the audit record carry one identity. Empty, the receipt gets its
	// own.
	RequestID   string   `json:"request_id,omitempty"`
	Target      Target   `json:"target"`
	Argv        []string `json:"argv"`
	Stdin       string   `json:"stdin,omitempty"`
	TimeoutMS   int64    `json:"timeout_ms,omitempty"`
	OutputBytes int64    `json:"output_bytes,omitempty"`
}
type ExecReceipt struct {
	RequestID     string       `json:"request_id"`
	Target        Target       `json:"target"`
	ObservedAfter *Observation `json:"observed_after,omitempty"`
	ScriptDigest  string       `json:"script_digest"`
	StartedAt     time.Time    `json:"started_at"`
	FinishedAt    time.Time    `json:"finished_at"`
	Stdout        string       `json:"stdout"`
	Stderr        string       `json:"stderr"`
	ExitCode      *int         `json:"exit_code,omitempty"`
	Truncated     bool         `json:"truncated"`
	RemoteState   string       `json:"remote_state"`
	// Supervision is how the run was supervised (SupervisionShGroup,
	// SupervisionShPID, SupervisionNone) and UnsupervisedReason why not.
	Supervision        string `json:"supervision,omitempty"`
	UnsupervisedReason string `json:"unsupervised_reason,omitempty"`
	// StreamCut is an unsupervised run this side cut at its deadline: it
	// may still run in the target.
	StreamCut bool `json:"stream_cut,omitempty"`
	// DeadlineRaced is a run whose timer fired as the command ended on its
	// own: the exit code says the command finished first.
	DeadlineRaced bool `json:"deadline_raced,omitempty"`
	// DeclaredTimeoutMS is the deadline asked for; EnforcedTimeoutSeconds
	// what the supervisor's timer ran, rounded up to whole seconds.
	DeclaredTimeoutMS      int64 `json:"declared_timeout_ms"`
	EnforcedTimeoutSeconds int64 `json:"enforced_timeout_seconds,omitempty"`
	// BootstrapExitCode and BootstrapClass say how the supervisor itself
	// failed before its first record; the class is a category, never text.
	BootstrapExitCode *int   `json:"bootstrap_exit_code,omitempty"`
	BootstrapClass    string `json:"bootstrap_class,omitempty"`
	// StartFailure says why the command never started: StartCommandUnavailable.
	StartFailure      string `json:"start_failure,omitempty"`
	TargetChanged     bool   `json:"target_changed"`
	EvidenceScope     string `json:"evidence_scope"`
	RedactionContract string `json:"redaction_contract"`
}

// DeleteRequest deletes one Pod for its controller to recreate. Target names
// the Pod and its UID; OwnerUID, when given, is the controller the caller saw
// own it, and a Pod now owned by another is not deleted.
type DeleteRequest struct {
	Target   Target `json:"target"`
	OwnerUID string `json:"owner_uid,omitempty"`
}

// WorkloadRequest names one workload by kind, name and the UID the caller saw.
// Replicas is the new count for a scale.
type WorkloadRequest struct {
	Namespace string `json:"namespace"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	UID       string `json:"uid"`
	Replicas  *int32 `json:"replicas,omitempty"`
}

// ActionReceipt is what one management action did.
type ActionReceipt struct {
	Op               string    `json:"op"`
	Namespace        string    `json:"namespace"`
	Kind             string    `json:"kind"`
	Name             string    `json:"name"`
	UID              string    `json:"uid"`
	PreviousReplicas *int32    `json:"previous_replicas,omitempty"`
	Replicas         *int32    `json:"replicas,omitempty"`
	RestartedAt      string    `json:"restarted_at,omitempty"`
	Applied          bool      `json:"applied"`
	At               time.Time `json:"at"`
}
