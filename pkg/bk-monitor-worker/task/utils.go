// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package task

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"

	pb "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/proto"
)

// EncodeMessage marshals the given task message and returns an encoded bytes.
func EncodeMessage(msg *TaskMessage) ([]byte, error) {
	if msg == nil {
		return nil, fmt.Errorf("cannot encode nil message")
	}
	return proto.Marshal(&pb.TaskMessage{
		Kind:         msg.Kind,
		Payload:      msg.Payload,
		Id:           msg.ID,
		Queue:        msg.Queue,
		Retry:        int32(msg.Retry),
		Retried:      int32(msg.Retried),
		ErrorMsg:     msg.ErrorMsg,
		LastFailedAt: msg.LastFailedAt,
		Timeout:      msg.Timeout,
		Deadline:     msg.Deadline,
		UniqueKey:    msg.UniqueKey,
		Retention:    msg.Retention,
		CompletedAt:  msg.CompletedAt,
	})
}

// DecodeMessage unmarshals the given bytes and returns a decoded task message.
func DecodeMessage(data []byte) (*TaskMessage, error) {
	var pbmsg pb.TaskMessage
	if err := proto.Unmarshal(data, &pbmsg); err != nil {
		return nil, err
	}
	return &TaskMessage{
		Kind:         pbmsg.GetKind(),
		Payload:      pbmsg.GetPayload(),
		ID:           pbmsg.GetId(),
		Queue:        pbmsg.GetQueue(),
		Retry:        int(pbmsg.GetRetry()),
		Retried:      int(pbmsg.GetRetried()),
		ErrorMsg:     pbmsg.GetErrorMsg(),
		LastFailedAt: pbmsg.GetLastFailedAt(),
		Timeout:      pbmsg.GetTimeout(),
		Deadline:     pbmsg.GetDeadline(),
		UniqueKey:    pbmsg.GetUniqueKey(),
		Retention:    pbmsg.GetRetention(),
		CompletedAt:  pbmsg.GetCompletedAt(),
	}, nil
}

const taskMetadataCtxKey = "taskMetadataKey"

// AddTaskMetadata2Context add task metadata to context
func AddTaskMetadata2Context(ctx context.Context, msg *TaskMessage, deadline time.Time) (context.Context, context.CancelFunc) {
	metadata := TaskMetadata{
		id:         msg.ID,
		maxRetry:   msg.Retry,
		retryCount: msg.Retried,
		qname:      msg.Queue,
	}
	_ctx := context.WithValue(ctx, taskMetadataCtxKey, metadata)
	return context.WithDeadline(_ctx, deadline)
}

// GetTaskIDByCtx get task id from context
func GetTaskIDByCtx(ctx context.Context) (id string, ok bool) {
	metadata, ok := ctx.Value(taskMetadataCtxKey).(TaskMetadata)
	if !ok {
		return "", false
	}
	return metadata.id, true
}
