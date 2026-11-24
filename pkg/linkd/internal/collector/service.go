// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package collector 编排告警源执行、可靠投递与采集进度提交。
package collector

import (
	"context"
	"errors"
	"fmt"

	"linkd/internal/pluginruntime"
)

// Executor 同步返回来源转换结果，不负责投递或推进进度。
type Executor interface {
	Execute(context.Context, pluginruntime.Request) (pluginruntime.Result, error)
}

// Publisher 等待所有事件持久接收后返回；失败可能已部分投递，调用方不得推进游标。
type Publisher interface {
	Publish(context.Context, pluginruntime.Request, pluginruntime.Result) error
}

// Service 是推送和定时拉取共用的执行顺序，仅重试插件明确返回的临时失败。
type Service struct {
	executions *Executions
	results    ResultStore
	executor   Executor
	publisher  Publisher
}

// ResultStore 保留首次执行输入与完整输出，重放必须复用相同配置、游标和批次顺序。
// Complete 必须原子保存候选进度与完成记录，避免恢复时重复提交旧进度。
type ResultStore interface {
	SaveRequest(context.Context, pluginruntime.Request) (pluginruntime.Request, error)
	BeginAttempt(context.Context, pluginruntime.Request) (bool, error)
	BeginPublication(context.Context, pluginruntime.Request) (int, error)
	LoadResult(context.Context, pluginruntime.Request) (*StoredResult, error)
	SaveResult(context.Context, pluginruntime.Request, pluginruntime.Result) (pluginruntime.Result, error)
	Complete(context.Context, pluginruntime.Request, pluginruntime.Result) error
}

// StoredResult 区分已计算与已终结请求；终结结果可能是成功，也可能是明确的插件失败。
// 终结记录存在期间重放不再产生副作用，失败结果不会提交采集进度。
type StoredResult struct {
	Result    pluginruntime.Result `json:"result"`
	Completed bool                 `json:"completed"`
}

// New 创建明确注入执行、投递和进度存储的来源服务。
func New(executor Executor, publisher Publisher, results ResultStore) *Service {
	return &Service{executor: executor, publisher: publisher, results: results}
}

// Run 完成插件计算、事件投递、进度提交。返回的 result 在错误时仅供诊断，不能据此响应成功。
// 空事件批次仍可推进游标；提交进度失败时事件可能已全部投递，重试策略须考虑重复投递。
func (s *Service) Run(ctx context.Context, request pluginruntime.Request) (pluginruntime.Result, error) {
	var result pluginruntime.Result
	if s.executions != nil {
		finish, acquired := s.executions.begin(request)
		if !acquired {
			return result, ErrExecutionBusy
		}
		defer finish()
	}
	var err error
	if err = ctx.Err(); err != nil {
		return result, err
	}
	found := false
	if s.results != nil {
		request, err = s.results.SaveRequest(ctx, request)
		if err != nil {
			return result, fmt.Errorf("persist source execution input: %w", err)
		}
		var stored *StoredResult
		stored, err = s.results.LoadResult(ctx, request)
		if err != nil {
			return result, fmt.Errorf("load plugin computation: %w", err)
		}
		if stored != nil {
			result, found = stored.Result, true
			if stored.Completed {
				if result.Failure != nil {
					return result, result.Failure
				}
				return result, nil
			}
		}
	}
	if !found {
		result, err = s.execute(ctx, request)
		if err != nil {
			var failure *pluginruntime.Failure
			if s.results != nil && ctx.Err() == nil && errors.As(err, &failure) {
				// 只终结执行器明确报告的插件失败；外部 I/O 结果未知不能据此丢弃请求。
				// 不保留失败返回中的部分事件或候选游标，避免恢复时误发布/误推进。
				result = pluginruntime.Result{Version: 1, RequestID: request.RequestID, TenantID: request.TenantID, SourceID: request.SourceID, Failure: failure}
				if finishErr := s.results.Complete(ctx, request, result); finishErr != nil {
					return result, errors.Join(fmt.Errorf("execute source plugin: %w", err), fmt.Errorf("record source failure: %w", finishErr))
				}
			}
			return result, fmt.Errorf("execute source plugin: %w", err)
		}
		if result.Failure != nil {
			return result, result.Failure
		}
		if err = ctx.Err(); err != nil {
			return result, err
		}
		if s.results != nil {
			result, err = s.results.SaveResult(ctx, request, result)
			if err != nil {
				return result, fmt.Errorf("save plugin computation before publication: %w", err)
			}
		}
	}
	if result.Failure != nil {
		return result, result.Failure
	}
	// 租约撤销或请求取消后，依赖可能仍返回计算结果；不得因此开始新的副作用阶段。
	// 检查不能撤回已经发出的 I/O，也不替代存储端的旧执行者隔离。
	if err = ctx.Err(); err != nil {
		return result, err
	}
	if len(result.Events) > 0 {
		attempt := 1
		if s.results != nil {
			attempt, err = s.results.BeginPublication(ctx, request)
			if err != nil {
				return result, fmt.Errorf("reserve source publication: %w", err)
			}
			if attempt == 0 {
				return s.failPublication(ctx, request, nil, "publication_attempts_exhausted")
			}
			if err = ctx.Err(); err != nil {
				return result, err
			}
		}
		if err = s.publisher.Publish(ctx, request, result); err != nil {
			var permanent *permanentPublicationError
			if ctx.Err() == nil && (attempt == 3 || errors.As(err, &permanent)) {
				return s.failPublication(ctx, request, err, "publication_failed")
			}
			return result, fmt.Errorf("publish source events: %w", err)
		}
	}
	if result.ProposedState != nil && s.results == nil {
		return result, fmt.Errorf("source result and progress store is not configured")
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	if s.results != nil {
		if err = s.results.Complete(ctx, request, result); err != nil {
			return result, fmt.Errorf("commit source progress and completion after publication: %w", err)
		}
	}
	return result, nil
}

// 终结当前请求不推进游标，也不能回滚可能已被 Kafka 接收的部分事件。
func (s *Service) failPublication(ctx context.Context, request pluginruntime.Request, cause error, code string) (pluginruntime.Result, error) {
	failure := &pluginruntime.Failure{Code: code, Type: "PublicationFailure"}
	result := pluginruntime.Result{Version: 1, RequestID: request.RequestID, TenantID: request.TenantID, SourceID: request.SourceID, Failure: failure}
	err := errors.Join(failure, cause)
	if s.results != nil {
		if finishErr := s.results.Complete(ctx, request, result); finishErr != nil {
			err = errors.Join(err, fmt.Errorf("record source publication failure: %w", finishErr))
		}
	}
	return result, err
}

// 仅重试执行器明确声明的临时失败，总计最多三次且共享原请求的截止时间。
// 网络断连、超时和容量拒绝不证明插件是否执行，不能据此自动重跑；
// 发布与进度提交也不在此重试，避免把计算重试扩大成整条链路重试。
func (s *Service) execute(ctx context.Context, request pluginruntime.Request) (pluginruntime.Result, error) {
	var result pluginruntime.Result
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if err = ctx.Err(); err != nil {
			return result, err
		}
		if s.results != nil {
			allowed, reserveErr := s.results.BeginAttempt(ctx, request)
			if reserveErr != nil {
				return result, fmt.Errorf("reserve plugin execution attempt: %w", reserveErr)
			}
			if !allowed {
				return result, &pluginruntime.Failure{Code: "attempts_exhausted", Type: "ExecutionAttemptsExhausted"}
			}
			if err = ctx.Err(); err != nil {
				return result, err
			}
		}
		result, err = s.executor.Execute(ctx, request)
		if err == nil && result.Failure != nil {
			err = result.Failure
		}
		var failure *pluginruntime.Failure
		if !errors.As(err, &failure) || failure.Code != "temporary_error" {
			return result, err
		}
	}
	return result, err
}
