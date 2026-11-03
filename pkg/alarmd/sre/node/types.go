// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

// Package node provides durable, non-replayed commands on current-cluster nodes.
package node

import (
	"context"
	"encoding/json"
	"time"

	"github.com/go-redis/redis/v8"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/sre/pod"
)

type Node struct {
	Name string `json:"name"`
	UID  string `json:"uid"`
}
type ExecRequest struct {
	Caller           Caller            `json:"-"`
	ExecutionID      string            `json:"execution_id"`
	Node             Node              `json:"node"`
	Mode             string            `json:"mode"`
	Cwd              string            `json:"cwd"`
	User             string            `json:"user"`
	Env              map[string]string `json:"env,omitempty"`
	Argv             []string          `json:"argv"`
	Stdin            string            `json:"stdin,omitempty"`
	TimeoutSeconds   int               `json:"timeout_seconds,omitempty"`
	OutputLimitBytes int               `json:"output_limit_bytes,omitempty"`
}
type GetRequest struct {
	ExecutionID  string `json:"execution_id"`
	OutputCursor string `json:"output_cursor,omitempty"`
	MaxBytes     int    `json:"max_bytes,omitempty"`
}
type CancelRequest struct {
	ExecutionID string `json:"execution_id"`
}
type Record struct {
	OutputIncomplete bool      `json:"output_incomplete,omitempty"`
	Caller           Caller    `json:"caller"`
	ExitCode         *int      `json:"exit_code,omitempty"`
	ExecutionID      string    `json:"execution_id"`
	Node             Node      `json:"node"`
	Mode             string    `json:"mode"`
	Namespace        string    `json:"namespace"`
	JobName          string    `json:"job_name"`
	JobUID           string    `json:"job_uid,omitempty"`
	PodName          string    `json:"pod_name,omitempty"`
	PodUID           string    `json:"pod_uid,omitempty"`
	ContainerID      string    `json:"container_id,omitempty"`
	ImageID          string    `json:"image_id,omitempty"`
	State            string    `json:"state"`
	CreatedAt        time.Time `json:"created_at"`
	Deadline         time.Time `json:"deadline"`
	Cleanup          string    `json:"cleanup"`
	Detail           string    `json:"detail,omitempty"`
}
type Result struct {
	Record
	CancelRequested   bool            `json:"cancel_requested"`
	Runner            json.RawMessage `json:"runner,omitempty"`
	OutputUnavailable bool            `json:"output_unavailable,omitempty"`
}
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string        { return e.Message }
func fail(code, message string) error { return &Error{code, message} }

type Options struct {
	Final                func(Record)
	Redis                redis.UniversalClient
	KeyPrefix            string
	FingerprintKeySource func() []byte
	DefaultNamespace     string
	Image                string
	DeploymentID         string
	ImagePullSecrets     []corev1.LocalObjectReference
	Client               kubernetes.Interface
	Executor             interface {
		Stream(context.Context, pod.StreamRequest) (pod.StreamResult, error)
	}
}
type Provider struct{ options Options }
type Caller struct {
	SessionID string `json:"session_id,omitempty"`
	RequestID string `json:"request_id,omitempty"`
	Operator  string `json:"operator,omitempty"`
	IssuedVia string `json:"issued_via,omitempty"`
}
