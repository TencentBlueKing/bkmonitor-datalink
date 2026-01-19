// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearch

import (
	"context"
	"errors"
	"io"
	"strings"

	elastic "github.com/olivere/elastic/v7"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/curl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

const (
	CausedByField = "caused_by"

	ReasonField = "reason"
	TypeField   = "type"
	IndexField  = "index"

	ThirdPartyErrType = "third_party_error"

	MsgLengthLimit = 500
)

func handleESError(ctx context.Context, url string, err error, shardsErrMsg string) error {
	if err == nil {
		return err
	}

	if errors.Is(err, io.EOF) {
		return nil
	}

	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return curl.HandleClientError(ctx, metadata.MsgQueryES, url, err)
	}

	var (
		msgBuilder strings.Builder
		esErr      *elastic.Error
	)

	msgLimit := func(msgLen int) int {
		return min(msgLen, MsgLengthLimit)
	}

	msgBuilder.WriteString("Elasticsearch error")
	if url != "" {
		msgBuilder.WriteString(" from ")
		msgBuilder.WriteString(url)
	}
	if errors.As(err, &esErr) {
		indices, reasonMsg, typeMsg := deepest(*esErr)
		if typeMsg != "" {
			msgBuilder.WriteString(": [")
			msgBuilder.WriteString(typeMsg)
			msgBuilder.WriteString("] ")
		}
		if reasonMsg != "" {
			msgBuilder.WriteString(reasonMsg[:msgLimit(len(reasonMsg))])
		}

		if len(indices) > 0 {
			msgBuilder.WriteString(" (indices: ")
			msgBuilder.WriteString(strings.Join(indices, ", "))
			msgBuilder.WriteString(")")
		}

	} else {
		msgBuilder.WriteString(": [")
		msgBuilder.WriteString(ThirdPartyErrType)
		msgBuilder.WriteString("] ")
		errMsg := err.Error()
		msgBuilder.WriteString(errMsg[:msgLimit(len(errMsg))])
	}

	// 附加 shard failures 信息
	if shardsErrMsg != "" {
		msgBuilder.WriteString("; shard failures: ")
		msgBuilder.WriteString(shardsErrMsg)
	}

	return metadata.NewMessage(metadata.MsgQueryES, "es 查询失败").Error(ctx, errors.New(msgBuilder.String()))
}

func deepest(esErr elastic.Error) (indices []string, reasonMsg string, typeMsg string) {
	if esErr.Details == nil {
		return
	}
	indices = extractIndices(esErr.Details.FailedShards)

	// 优先使用 caused_by
	if esErr.Details.CausedBy != nil {
		reasonMsg, typeMsg = extractReasonAndType(esErr.Details.CausedBy, true)
	}
	if reasonMsg == "" && typeMsg == "" && len(esErr.Details.RootCause) > 0 {
		reasonMsg, typeMsg = extractFromErrorDetails(esErr.Details.RootCause[0])
	}
	if reasonMsg == "" && typeMsg == "" {
		reasonMsg, typeMsg = esErr.Details.Reason, esErr.Details.Type
	}
	return
}

func extractReasonAndType(cause map[string]any, recursive bool) (reasonMsg string, typeMsg string) {
	if cause == nil {
		return
	}

	if recursive {
		// 一直往下找最深的 caused_by(优先返回最深的)
		for {
			next, ok := cause[CausedByField].(map[string]any)
			if !ok {
				break
			}
			cause = next
		}
	}

	reasonMsg, _ = cause[ReasonField].(string)
	typeMsg, _ = cause[TypeField].(string)
	return
}

func extractFromErrorDetails(details *elastic.ErrorDetails) (reasonMsg string, typeMsg string) {
	if details == nil {
		return
	}
	return details.Reason, details.Type
}

func extractIndices(failedShards []map[string]any) []string {
	if len(failedShards) == 0 {
		return nil
	}

	var indices []string
	for _, shardInfo := range failedShards {
		if index, ok := shardInfo[IndexField].(string); ok {
			indices = append(indices, index)
		}
	}
	return indices
}

// extractShardFailuresInfo 从 shard failures 中提取错误信息
func extractShardFailuresInfo(failures []*elastic.ShardOperationFailedException) (indices []string, reasonMsg string, typeMsg string) {
	if len(failures) == 0 {
		return
	}

	for _, failure := range failures {
		if failure.Index != "" {
			indices = append(indices, failure.Index)
		}
		// 只从第一个有效的 failure 提取错误原因
		if reasonMsg == "" && failure.Reason != nil {
			reasonMsg, typeMsg = extractReasonAndType(failure.Reason, true)
		}
	}
	return
}

func handleEsShardsErr(failures []*elastic.ShardOperationFailedException) string {
	if len(failures) == 0 {
		return ""
	}

	indices, reasonMsg, typeMsg := extractShardFailuresInfo(failures)
	if reasonMsg == "" && typeMsg == "" {
		return ""
	}

	var msgBuilder strings.Builder
	if typeMsg != "" {
		msgBuilder.WriteString("[")
		msgBuilder.WriteString(typeMsg)
		msgBuilder.WriteString("] ")
	}
	if reasonMsg != "" {
		msgLimit := min(len(reasonMsg), MsgLengthLimit)
		msgBuilder.WriteString(reasonMsg[:msgLimit])
	}
	if len(indices) > 0 {
		msgBuilder.WriteString(" (indices: ")
		msgBuilder.WriteString(strings.Join(indices, ", "))
		msgBuilder.WriteString(")")
	}
	return msgBuilder.String()
}
