// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.
package node

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kubeclient"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/sre/pod"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/sre/redact"
)

const runnerPath = "/data/bkmonitor/alarmd-node-runner"
const identityLabel = "alarmd.blueking.com/node-execution"

func NewInCluster(o Options) (*Provider, error) {
	cfg, err := kubeclient.InCluster()
	if err != nil {
		return nil, err
	}
	if o.DefaultNamespace == "" {
		b, e := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
		if e != nil {
			return nil, e
		}
		o.DefaultNamespace = strings.TrimSpace(string(b))
	}
	o.Client, err = kubernetes.NewForConfig(kubeclient.WithRate(cfg))
	if err != nil {
		return nil, err
	}
	o.Executor, err = pod.NewKubernetesExecutor(cfg)
	if err != nil {
		return nil, err
	}
	if o.Image == "" {
		name, err := os.Hostname()
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		self, err := o.Client.CoreV1().Pods(o.DefaultNamespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		o.ImagePullSecrets = append([]corev1.LocalObjectReference(nil), self.Spec.ImagePullSecrets...)
		// Prefer the container whose command actually starts alarmd; ambiguous
		// multi-container pods must configure their image explicitly.
		for _, c := range self.Spec.Containers {
			if len(self.Spec.Containers) == 1 || (len(c.Command) > 0 && strings.HasSuffix(c.Command[0], "/alarmd")) {
				if o.Image != "" {
					return nil, errors.New("ambiguous alarmd container")
				}
				o.Image = c.Image
			}
		}
	}
	return New(o)
}
func (p *Provider) label() string {
	s := sha256.Sum256([]byte(p.options.DeploymentID))
	return hex.EncodeToString(s[:16])
}
func (p *Provider) job(r Record) *batchv1.Job {
	zero := int32(0)
	one := int32(1)
	ttl := int32(300)
	deadline := int64(time.Until(r.Deadline).Seconds())
	no := false
	yes := true
	root := int64(0)
	labels := map[string]string{identityLabel: p.label(), "alarmd.blueking.com/execution-id": r.ExecutionID}
	container := corev1.Container{
		Name: "runner", Image: p.options.Image, ImagePullPolicy: corev1.PullIfNotPresent,
		Command:         []string{runnerPath, "serve", "--execution-id", r.ExecutionID, "--mode", r.Mode, "--retention-seconds", "900", "--idle-seconds", "60"},
		SecurityContext: &corev1.SecurityContext{Privileged: &yes, RunAsUser: &root, RunAsNonRoot: &no},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("50m"), corev1.ResourceMemory: resource.MustParse("64Mi")},
			Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1"), corev1.ResourceMemory: resource.MustParse("1Gi"), corev1.ResourceEphemeralStorage: resource.MustParse("128Mi")},
		},
		VolumeMounts: []corev1.VolumeMount{{Name: "host", MountPath: "/host", ReadOnly: r.Mode == "filesystem"}, {Name: "state", MountPath: "/run/alarmd-node"}},
	}
	template := corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{Labels: labels},
		Spec: corev1.PodSpec{
			ImagePullSecrets: p.options.ImagePullSecrets, NodeName: r.Node.Name, HostPID: r.Mode == "host", RestartPolicy: corev1.RestartPolicyNever,
			AutomountServiceAccountToken: &no,
			SecurityContext:              &corev1.PodSecurityContext{RunAsUser: &root, RunAsGroup: &root, RunAsNonRoot: &no},
			Containers:                   []corev1.Container{container},
			Volumes: []corev1.Volume{
				{Name: "host", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/"}}},
				{Name: "state", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{SizeLimit: resource.NewQuantity(128<<20, resource.BinarySI)}}},
			},
		},
	}
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: r.JobName, Namespace: r.Namespace, Labels: labels},
		Spec:       batchv1.JobSpec{BackoffLimit: &zero, Parallelism: &one, Completions: &one, ActiveDeadlineSeconds: &deadline, TTLSecondsAfterFinished: &ttl, Template: template},
	}
}
func (p *Provider) prepare(request ExecRequest, r Record) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	failPreparation := func(detail string) {
		next := r
		next.State = "unknown"
		next.Detail = detail
		_ = p.replace(context.Background(), r, next, false)
	}
	job, err := p.options.Client.BatchV1().Jobs(r.Namespace).Create(ctx, p.job(r), metav1.CreateOptions{})
	createErr := err
	if err != nil { // A create whose response was lost is recovered by name, never overwritten.
		job, err = p.options.Client.BatchV1().Jobs(r.Namespace).Get(ctx, r.JobName, metav1.GetOptions{})
	}
	if err != nil {
		failPreparation("carrier creation unconfirmed: " + safeError(createErr))
		return
	}
	if job.Labels[identityLabel] != p.label() || job.Labels["alarmd.blueking.com/execution-id"] != r.ExecutionID || job.Spec.Template.Spec.NodeName != r.Node.Name || job.UID == "" {
		failPreparation("carrier identity mismatch")
		return
	}
	next := r
	next.JobUID = string(job.UID)
	if err = p.replace(ctx, r, next, false); err != nil {
		return
	}
	r = next
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	startupDetail := "runner did not become ready"
	for {
		_, _, cancelled, e := p.read(ctx, r.ExecutionID)
		if e != nil {
			return
		}
		if cancelled {
			next := r
			next.State = "cancelled"
			next.Detail = "cancelled before dispatch"
			if p.replace(ctx, r, next, false) == nil {
				p.finish(ctx, next)
			}
			return
		}
		pods, e := p.options.Client.CoreV1().Pods(r.Namespace).List(ctx, metav1.ListOptions{LabelSelector: "job-name=" + r.JobName})
		if e != nil {
			startupDetail = safeError(e)
		}
		if e == nil {
			for _, candidate := range pods.Items {
				for _, s := range candidate.Status.ContainerStatuses {
					if s.State.Waiting != nil {
						startupDetail = safeError(errors.New(s.State.Waiting.Reason + ": " + s.State.Waiting.Message))
					}
					if s.State.Terminated != nil {
						startupDetail = safeError(errors.New(s.State.Terminated.Reason + ": " + s.State.Terminated.Message))
					}
				}
				for _, c := range candidate.Status.Conditions {
					if c.Status == corev1.ConditionFalse && c.Message != "" {
						startupDetail = safeError(errors.New(c.Reason + ": " + c.Message))
					}
				}
				if candidate.Spec.NodeName != r.Node.Name || candidate.Status.Phase != corev1.PodRunning || candidate.DeletionTimestamp != nil {
					continue
				}
				owner := metav1.GetControllerOf(&candidate)
				if owner == nil || owner.UID != job.UID || owner.Kind != "Job" {
					continue
				}
				for _, status := range candidate.Status.ContainerStatuses {
					if status.Name != "runner" || status.State.Running == nil || status.ContainerID == "" || status.ImageID == "" {
						continue
					}
					next = r
					next.PodName = candidate.Name
					next.PodUID = string(candidate.UID)
					next.ContainerID = status.ContainerID
					next.ImageID = status.ImageID
					next.State = "dispatching"
					if err = p.verify(ctx, next); err != nil {
						failPreparation("target identity changed before dispatch")
						return
					}
					// Running means the container process started, not that its
					// local socket accepts controls. Probe with a replay-safe read
					// before consuming the single dispatch opportunity.
					probeCtx, probeCancel := context.WithTimeout(ctx, 3*time.Second)
					_, probeErr := p.control(probeCtx, next, map[string]any{"action": "get", "execution_id": r.ExecutionID, "max_bytes": 1})
					probeCancel()
					if probeErr != nil {
						startupDetail = "runner control unavailable: " + safeError(probeErr)
						continue
					}
					if err = p.replace(ctx, r, next, true); err != nil {
						return
					}
					r = next
					raw, _ := json.Marshal(request)
					var wire map[string]any
					_ = json.Unmarshal(raw, &wire)
					delete(wire, "node")
					wire["action"] = "start"
					response, startErr := p.control(ctx, r, wire)
					err = startErr
					if err != nil {
						failPreparation("dispatch outcome unknown; command will not be replayed")
						return
					}
					var ack struct {
						Error        string `json:"error"`
						ControlError bool   `json:"control_error"`
					}
					_ = json.Unmarshal(response, &ack)
					if ack.Error != "" && ack.ControlError {
						next = r
						next.State = "failed"
						next.Detail = "runner rejected start"
						if p.replace(ctx, r, next, false) == nil {
							p.finish(ctx, next)
						}
						return
					}
					_, _ = p.Get(ctx, GetRequest{ExecutionID: r.ExecutionID})
					return
				}
			}
		}
		select {
		case <-ctx.Done():
			failPreparation("runner startup deadline exceeded: " + startupDetail)
			return
		case <-ticker.C:
		}
	}
}
func (p *Provider) verify(ctx context.Context, r Record) error {
	n, err := p.options.Client.CoreV1().Nodes().Get(ctx, r.Node.Name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if string(n.UID) != r.Node.UID {
		return errors.New("node UID changed")
	}
	job, err := p.options.Client.BatchV1().Jobs(r.Namespace).Get(ctx, r.JobName, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if string(job.UID) != r.JobUID || job.Labels[identityLabel] != p.label() {
		return errors.New("job UID changed")
	}
	candidate, err := p.options.Client.CoreV1().Pods(r.Namespace).Get(ctx, r.PodName, metav1.GetOptions{})
	if err != nil {
		return err
	}
	owner := metav1.GetControllerOf(candidate)
	if string(candidate.UID) != r.PodUID || candidate.Spec.NodeName != r.Node.Name || owner == nil || string(owner.UID) != r.JobUID || owner.Kind != "Job" || candidate.DeletionTimestamp != nil {
		return errors.New("pod identity changed")
	}
	for _, s := range candidate.Status.ContainerStatuses {
		if s.Name == "runner" && s.ContainerID == r.ContainerID && s.ImageID == r.ImageID && s.State.Running != nil {
			return nil
		}
	}
	return errors.New("container identity changed")
}

type boundedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *boundedBuffer) Write(v []byte) (int, error) {
	if b.Len()+len(v) > b.limit {
		return 0, errors.New("runner control response exceeded bound")
	}
	return b.Buffer.Write(v)
}
func (p *Provider) control(ctx context.Context, r Record, request any) (json.RawMessage, error) {
	if err := p.verify(ctx, r); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	out := &boundedBuffer{limit: 2 << 20}
	stderr := &boundedBuffer{limit: 4096}
	result, err := p.options.Executor.Stream(ctx, pod.StreamRequest{Target: pod.Target{Namespace: r.Namespace, Pod: r.PodName, Container: "runner"}, Argv: []string{runnerPath, "control"}, Stdin: bytes.NewReader(raw), Stdout: out, Stderr: stderr})
	if err != nil {
		return nil, err
	}
	if result.ExitCode == nil || *result.ExitCode != 0 {
		return nil, errors.New("runner control failed")
	}
	if !json.Valid(out.Bytes()) {
		return nil, errors.New("invalid runner response")
	}
	if err = p.verify(ctx, r); err != nil {
		return nil, err
	}
	return append(json.RawMessage(nil), out.Bytes()...), nil
}
func isNotFound(err error) bool { return apierrors.IsNotFound(err) }

func safeError(err error) string {
	if err == nil {
		return "unknown API outcome"
	}
	message := redact.New().Text(err.Error())
	if len(message) > 2048 {
		message = message[:2048] + " [truncated]"
	}
	return message
}

var _ io.Writer = (*boundedBuffer)(nil)
