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
	"fmt"
	"io"
	"reflect"
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

func extractESResult(err error, res *elastic.SearchResult) ([]*elastic.ShardOperationFailedException, error) {
	if res != nil {
		if err == nil && res.Error != nil {
			err = &elastic.Error{
				Status:  res.Status,
				Details: res.Error,
			}
		}
		if res.Shards != nil && len(res.Shards.Failures) > 0 {
			return res.Shards.Failures, err
		}
	} else if err == nil {
		err = fmt.Errorf("unexpected nil SearchResult")
	}
	return nil, err
}

func handleESError(ctx context.Context, url string, err error, res *elastic.SearchResult) error {
	shardFailures, err := extractESResult(err, res)

	if err == nil && len(shardFailures) == 0 {
		return nil
	}

	if errors.Is(err, io.EOF) && len(shardFailures) == 0 {
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

	// 处理 ES 错误
	if err != nil {
		if errors.As(err, &esErr) && esErr != nil {
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
		} else if !isTypedNilError(err) {
			msgBuilder.WriteString(": [")
			msgBuilder.WriteString(ThirdPartyErrType)
			msgBuilder.WriteString("] ")
			errMsg := err.Error()
			msgBuilder.WriteString(errMsg[:msgLimit(len(errMsg))])
		}
	}

	// 处理 shard failures
	if len(shardFailures) > 0 {
		indices, reasonMsg, typeMsg := extractShardFailuresInfo(shardFailures)
		if reasonMsg != "" || typeMsg != "" {
			if err != nil {
				msgBuilder.WriteString("; ")
			} else {
				msgBuilder.WriteString(": ")
			}
			msgBuilder.WriteString("shard failures: ")
			if typeMsg != "" {
				msgBuilder.WriteString("[")
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
		}
	}

	// 文本照旧；ES 自己的错误（含其状态码）和传输错误作为原因保留，供失败响应按类型分类。
	var cause error
	if err != nil && !isTypedNilError(err) {
		cause = withBackendStatus(err)
	}
	return metadata.NewMessage(metadata.MsgQueryES, "es 查询失败").Error(ctx, metadata.ErrorWithCauses(msgBuilder.String(), cause))
}

// withBackendStatus 在 err 带有 ES 自己的错误（*elastic.Error）时，把它的状态码作为类型化原因附上，
// 文本不变；没有时原样返回。凡是把 ES 客户端错误交给上层的地方都经过它。
//
// 聚合桶数超限（too_many_buckets_exception）例外：ES 把它报成 503，但它说的是这次查询要的桶太多，
// 换一个时间范围或聚合粒度才能成功，原样重试只会再超一次。所以不附 ES 的状态码，失败按查询本身的问题（400）
// 归类，调用方不把它当后端故障去重试。
func withBackendStatus(err error) error {
	var esErr *elastic.Error
	if err == nil || !errors.As(err, &esErr) || esErr == nil {
		return err
	}
	if exceedsBucketLimit(esErr.Details) {
		return err
	}
	return metadata.ErrorWithCauses(err.Error(), err, &metadata.BackendStatusError{Status: esErr.Status})
}

// bucketLimitErrType 是 ES 在一次聚合要的桶超过 search.max_buckets 时报的错误类型。
const bucketLimitErrType = "too_many_buckets_exception"

// exceedsBucketLimit 判断 ES 的错误是不是桶数超限：错误本身、caused_by 链上或 root_cause 里任一处是这个类型。
func exceedsBucketLimit(details *elastic.ErrorDetails) bool {
	if details == nil {
		return false
	}
	if details.Type == bucketLimitErrType || causedByBucketLimit(details.CausedBy) {
		return true
	}
	for _, cause := range details.RootCause {
		if exceedsBucketLimit(cause) {
			return true
		}
	}
	return false
}

func causedByBucketLimit(cause map[string]interface{}) bool {
	for depth := 0; cause != nil && depth < 16; depth++ {
		if kind, _ := cause[TypeField].(string); kind == bucketLimitErrType {
			return true
		}
		next, _ := cause["caused_by"].(map[string]interface{})
		cause = next
	}
	return false
}

func isTypedNilError(err error) bool {
	if err == nil {
		return true
	}
	v := reflect.ValueOf(err)
	switch v.Kind() {
	case reflect.Ptr, reflect.Map, reflect.Slice, reflect.Interface, reflect.Func, reflect.Chan:
		return v.IsNil()
	default:
		return false
	}
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
