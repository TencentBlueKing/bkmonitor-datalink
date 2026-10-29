// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

// Package noderunner implements the single-execution, local control protocol
// used by ephemeral node diagnostic Jobs. It never retries user commands.
package noderunner

import "time"

const DefaultSocket = "/run/alarmd-node/runner.sock"

type Request struct {
	Action           string            `json:"action"`
	ExecutionID      string            `json:"execution_id"`
	Mode             string            `json:"mode,omitempty"`
	Cwd              string            `json:"cwd,omitempty"`
	User             string            `json:"user,omitempty"`
	Env              map[string]string `json:"env,omitempty"`
	Argv             []string          `json:"argv,omitempty"`
	Stdin            string            `json:"stdin,omitempty"`
	TimeoutSeconds   int               `json:"timeout_seconds,omitempty"`
	OutputLimitBytes int               `json:"output_limit_bytes,omitempty"`
	OutputCursor     string            `json:"output_cursor,omitempty"`
	MaxBytes         int               `json:"max_bytes,omitempty"`
}

type Chunk struct {
	Stream string `json:"stream"`
	Data   string `json:"data"`
	Offset int64  `json:"offset"`
}

type Response struct {
	ExecutionID         string     `json:"execution_id"`
	State               string     `json:"state"`
	ExitCode            *int       `json:"exit_code,omitempty"`
	Signal              string     `json:"signal,omitempty"`
	Output              []Chunk    `json:"output,omitempty"`
	NextCursor          string     `json:"next_cursor"`
	TotalBytes          int64      `json:"total_bytes"`
	DroppedBytes        int64      `json:"dropped_bytes"`
	Truncated           bool       `json:"truncated"`
	CancelRequested     bool       `json:"cancel_requested"`
	ConfirmedTerminated bool       `json:"confirmed_terminated"`
	ControlError        bool       `json:"control_error,omitempty"`
	Error               string     `json:"error,omitempty"`
	StartedAt           *time.Time `json:"started_at,omitempty"`
	FinishedAt          *time.Time `json:"finished_at,omitempty"`
	RetainUntil         *time.Time `json:"retain_until,omitempty"`
}
