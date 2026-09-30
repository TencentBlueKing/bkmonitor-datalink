// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package viewstream

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream/pb"
)

const (
	EvidenceRequestBytes  = 64 << 10
	EvidenceResponseBytes = 2 << 20
	EvidenceTimeout       = 3 * time.Second
	EvidenceConcurrency   = 4
)

// SetEvidenceHandler binds the runtime's authenticated diagnostic handler.
// This layer only enforces transport budgets; it must not authenticate with
// Connect's production Redis admission dependency. A nil handler disables the
// RPC without changing the control stream. Bind during runtime startup.
func (server *Server) SetEvidenceHandler(handler func(context.Context, *pb.EvidenceRequest) (*pb.EvidenceResult, error)) {
	server.evidenceMu.Lock()
	server.evidenceHandler = handler
	server.evidenceMu.Unlock()
}

// ReadEvidence runs independently of Connect. Cancellation returns promptly,
// but the slot stays occupied until the handler actually exits: an uncooperative
// handler can consume at most EvidenceConcurrency goroutines, not an unbounded
// series of abandoned executions. Runtime readers must observe their context.
func (server *Server) ReadEvidence(ctx context.Context, request *pb.EvidenceRequest) (*pb.EvidenceResult, error) {
	ctx, cancel := context.WithTimeout(ctx, EvidenceTimeout)
	defer cancel()
	server.evidenceMu.RLock()
	handler := server.evidenceHandler
	server.evidenceMu.RUnlock()
	if handler == nil {
		return nil, status.Error(codes.Unimplemented, "diagnostic evidence is not configured")
	}
	if request == nil {
		return nil, status.Error(codes.InvalidArgument, "diagnostic request is required")
	}
	if proto.Size(request) > EvidenceRequestBytes {
		return nil, status.Error(codes.ResourceExhausted, "diagnostic request exceeds the byte limit")
	}
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	select {
	case server.evidenceSlots <- struct{}{}:
	default:
		return nil, status.Error(codes.ResourceExhausted, "diagnostic request slots are busy")
	}
	type outcome struct {
		result *pb.EvidenceResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		defer func() { <-server.evidenceSlots }()
		if err := ctx.Err(); err != nil {
			done <- outcome{err: status.FromContextError(err).Err()}
			return
		}
		result, err := handler(ctx, request)
		if err == nil {
			switch {
			case result == nil:
				err = status.Error(codes.Internal, "diagnostic handler returned no response")
			case proto.Size(result) > EvidenceResponseBytes:
				result = nil
				err = status.Error(codes.ResourceExhausted, "diagnostic response exceeds the byte limit")
			}
		}
		done <- outcome{result: result, err: err}
	}()
	select {
	case <-ctx.Done():
		return nil, status.FromContextError(ctx.Err()).Err()
	case completed := <-done:
		if err := ctx.Err(); err != nil {
			return nil, status.FromContextError(err).Err()
		}
		return completed.result, completed.err
	}
}
