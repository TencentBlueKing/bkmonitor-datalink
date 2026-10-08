// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package policy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"
	"unicode"

	"linkd/internal/domain"
)

// MaxPageSize 限制每页完整配置数量；后端同时限制单对象和响应字节数。
const MaxPageSize = 16

var (
	// ErrAccess 表示来源任务或租户授权校验失败，运行方不能按普通策略异常跳过。
	ErrAccess = errors.New("policy access denied")
	// ErrInvalid 表示发布参数或配置不满足已声明的约束。
	ErrInvalid = errors.New("invalid policy request")
	// ErrNotFound 表示当前租户中的策略或发布不存在。
	ErrNotFound = errors.New("policy not found")
	// ErrConflict 表示版本、操作身份或不可变发布内容冲突。
	ErrConflict = errors.New("policy version or operation conflict")
)

// Documents 提供三个集合的 create-only/CAS 和前缀分页，不承诺跨对象事务。
// List 不得返回部分成功；prefix 为空只供控制面全局待发布恢复扫描。
type Documents interface {
	Get(context.Context, string, string) (json.RawMessage, string, error)
	Put(context.Context, string, string, string, json.RawMessage) error
	List(context.Context, string, string, string, int) ([]json.RawMessage, error)
}

// Scope 固定策略的租户与类型，不能以 EventSource 代替租户范围。
type Scope struct {
	TenantID string `json:"bk_tenant_id"`
	Kind     Kind   `json:"type"`
}

// Validate 拒绝无租户或未知类型的作用域。
func (s Scope) Validate() error {
	if err := domain.ValidateIdentityPart("bk_tenant_id", s.TenantID, 64); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if !s.Kind.Valid() {
		return fmt.Errorf("%w: invalid policy type", ErrInvalid)
	}
	return nil
}

func (s Scope) prefix() string {
	return base64.RawURLEncoding.EncodeToString([]byte(s.TenantID)) + ":" + string(s.Kind) + ":"
}

func (s Scope) key(id string) string { return s.prefix() + id }

// Release 是不可变配置与编译摘要。CreatedAt 来自持久化操作意图，重试不使用当前时间生成身份。
type Release struct {
	Scope
	ID            string          `json:"id"`
	Version       int64           `json:"version"`
	OperationID   string          `json:"operation_id"`
	RequestDigest string          `json:"request_digest"`
	Spec          json.RawMessage `json:"spec"`
	Compiled      Summary         `json:"compiled"`
	Deleted       bool            `json:"deleted"`
	CreatedAt     time.Time       `json:"created_at"`
}

// Record 的 Pending 先于不可变发布保存；Published 只指向已经确认可读的 Release。
type Record struct {
	Scope
	ID        string          `json:"id"`
	Revision  int64           `json:"revision"`
	Published int64           `json:"published"`
	Compiled  Summary         `json:"compiled"`
	Spec      json.RawMessage `json:"spec"`
	Deleted   bool            `json:"deleted"`
	Pending   *Release        `json:"pending,omitempty"`
}

// ApplyRequest 使用 expected_version 防止旧同步覆盖新版本，operation_id 实现跨重试幂等。
type ApplyRequest struct {
	Scope
	SchemaVersion   int             `json:"schema_version"`
	ID              string          `json:"id"`
	ExpectedVersion int64           `json:"expected_version"`
	OperationID     string          `json:"operation_id"`
	Spec            json.RawMessage `json:"spec"`
	Deleted         bool            `json:"deleted,omitempty"`
}

type operation struct {
	Digest    string    `json:"digest"`
	Version   int64     `json:"version"`
	CreatedAt time.Time `json:"created_at"`
}

// Service 统一配置发布/删除和部分成功恢复，不执行运行时策略副作用。
type Service struct {
	docs Documents
	now  func() time.Time
}

// NewService 装配由调用方持有生命周期的文档存储。
func NewService(docs Documents) *Service {
	return &Service{docs: docs, now: func() time.Time { return time.Now().UTC() }}
}

func validatePolicyID(id string) error {
	if err := domain.ValidateIdentityPart("policy id", id, 80); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return nil
}

func operationKey(scope Scope, id, operationID string) string {
	digest := sha256.Sum256([]byte(operationID))
	return scope.key(id) + ":" + hex.EncodeToString(digest[:])
}

func releaseKey(scope Scope, id string, version int64) string {
	return scope.key(id) + ":" + strconv.FormatInt(version, 10)
}

// Apply 保存操作身份后预留编辑版本，再创建发布并推进 published 指针。任意中间失败可原身份重试。
func (s *Service) Apply(ctx context.Context, request ApplyRequest) (Release, error) {
	if err := ctx.Err(); err != nil {
		return Release{}, err
	}
	if err := request.Validate(); err != nil {
		return Release{}, err
	}
	if err := validatePolicyID(request.ID); err != nil {
		return Release{}, err
	}
	if request.SchemaVersion != 1 || request.ExpectedVersion < 0 || request.ExpectedVersion >= 1<<53 || request.OperationID == "" || len(request.OperationID) > 128 {
		return Release{}, fmt.Errorf("%w: schema_version=1, bounded version and operation_id are required", ErrInvalid)
	}
	for _, r := range request.OperationID {
		if unicode.IsControl(r) {
			return Release{}, fmt.Errorf("%w: operation_id contains control character", ErrInvalid)
		}
	}
	compiled, err := Compile(request.Kind, request.Spec)
	if err != nil {
		return Release{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	request.Spec = compiled.Canonical
	raw, err := json.Marshal(request)
	if err != nil {
		return Release{}, err
	}
	hash := sha256.Sum256(raw)
	digest := hex.EncodeToString(hash[:])
	op, err := s.ensureOperation(ctx, request, digest)
	if err != nil {
		return Release{}, err
	}
	current, token, err := s.get(ctx, request.Scope, request.ID)
	if errors.Is(err, ErrNotFound) {
		current = Record{Scope: request.Scope, ID: request.ID}
		token = ""
	} else if err != nil {
		return Release{}, err
	}
	// 重试旧操作时返回它原来的发布，而不是把后来版本伪装成当前操作的结果。
	if current.Published >= op.Version {
		release, err := s.GetRelease(ctx, request.Scope, request.ID, op.Version)
		if err != nil {
			return Release{}, err
		}
		if release.OperationID != request.OperationID || release.RequestDigest != digest {
			return Release{}, ErrConflict
		}
		return release, nil
	}
	if current.Pending != nil {
		if current.Pending.OperationID != request.OperationID || current.Pending.RequestDigest != digest {
			return Release{}, ErrConflict
		}
		return s.finish(ctx, current, token)
	}
	if current.Revision != request.ExpectedVersion {
		return Release{}, ErrConflict
	}
	pending := Release{Scope: request.Scope, ID: request.ID, Version: op.Version, OperationID: request.OperationID, RequestDigest: digest, Spec: compiled.Canonical, Compiled: compiled.Summary, Deleted: request.Deleted, CreatedAt: op.CreatedAt}
	current.Revision = op.Version
	current.Pending = &pending
	body, err := json.Marshal(current)
	if err != nil {
		return Release{}, err
	}
	if err := s.docs.Put(ctx, "records", request.key(request.ID), token, body); err != nil {
		return Release{}, err
	}
	current, token, err = s.get(ctx, request.Scope, request.ID)
	if err != nil {
		return Release{}, err
	}
	// 其他恢复者可能已推进当前操作，甚至又发布了下一版本；只能返回本次操作的快照。
	if current.Published >= op.Version {
		release, err := s.GetRelease(ctx, request.Scope, request.ID, op.Version)
		if err != nil {
			return Release{}, err
		}
		if release.OperationID != request.OperationID || release.RequestDigest != digest {
			return Release{}, ErrConflict
		}
		return release, nil
	}
	if current.Pending == nil || current.Pending.OperationID != request.OperationID || current.Pending.RequestDigest != digest {
		return Release{}, ErrConflict
	}
	return s.finish(ctx, current, token)
}

func (s *Service) ensureOperation(ctx context.Context, request ApplyRequest, digest string) (operation, error) {
	key := operationKey(request.Scope, request.ID, request.OperationID)
	raw, _, err := s.docs.Get(ctx, "operations", key)
	if errors.Is(err, ErrNotFound) {
		op := operation{Digest: digest, Version: request.ExpectedVersion + 1, CreatedAt: s.now()}
		body, marshalErr := json.Marshal(op)
		if marshalErr != nil {
			return operation{}, marshalErr
		}
		err = s.docs.Put(ctx, "operations", key, "", body)
		if err == nil {
			return op, nil
		}
		if !errors.Is(err, ErrConflict) {
			return operation{}, err
		}
		raw, _, err = s.docs.Get(ctx, "operations", key)
	}
	if err != nil {
		return operation{}, err
	}
	var op operation
	if err := json.Unmarshal(raw, &op); err != nil {
		return operation{}, err
	}
	if op.Digest != digest || op.Version != request.ExpectedVersion+1 {
		return operation{}, ErrConflict
	}
	return op, nil
}

func (s *Service) get(ctx context.Context, scope Scope, id string) (Record, string, error) {
	if err := scope.Validate(); err != nil {
		return Record{}, "", err
	}
	if err := validatePolicyID(id); err != nil {
		return Record{}, "", err
	}
	raw, token, err := s.docs.Get(ctx, "records", scope.key(id))
	if err != nil {
		return Record{}, "", err
	}
	var result Record
	if err := json.Unmarshal(raw, &result); err != nil {
		return Record{}, "", err
	}
	if result.Scope != scope || result.ID != id {
		return Record{}, "", fmt.Errorf("%w: stored policy scope mismatch", ErrAccess)
	}
	return result, token, nil
}

// Get 返回当前发布指针和待发布状态，不在只读请求中修改配置。
func (s *Service) Get(ctx context.Context, scope Scope, id string) (Record, error) {
	result, _, err := s.get(ctx, scope, id)
	return result, err
}

// GetRelease 按精确版本读取，绝不使用最新版本回退。
func (s *Service) GetRelease(ctx context.Context, scope Scope, id string, version int64) (Release, error) {
	if err := scope.Validate(); err != nil {
		return Release{}, err
	}
	if err := validatePolicyID(id); err != nil {
		return Release{}, err
	}
	if version < 1 {
		return Release{}, fmt.Errorf("%w: release version must be positive", ErrInvalid)
	}
	raw, _, err := s.docs.Get(ctx, "releases", releaseKey(scope, id, version))
	if err != nil {
		return Release{}, err
	}
	var result Release
	if err := json.Unmarshal(raw, &result); err != nil {
		return Release{}, err
	}
	if result.Scope != scope || result.ID != id || result.Version != version {
		return Release{}, fmt.Errorf("%w: stored release scope mismatch", ErrAccess)
	}
	return result, nil
}

// List 按策略 ID 分页，包括 tombstone；调用方不能通过分页切换租户或类型。
func (s *Service) List(ctx context.Context, scope Scope, after string, limit int) ([]Record, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	if after != "" {
		if err := validatePolicyID(after); err != nil {
			return nil, err
		}
	}
	if limit < 1 || limit > MaxPageSize {
		return nil, fmt.Errorf("%w: page size must be 1..16", ErrInvalid)
	}
	rows, err := s.docs.List(ctx, "records", scope.prefix(), scope.key(after), limit)
	if err != nil {
		return nil, err
	}
	result := make([]Record, 0, len(rows))
	for _, row := range rows {
		var record Record
		if err := json.Unmarshal(row, &record); err != nil {
			return nil, err
		}
		if record.Scope != scope {
			return nil, ErrAccess
		}
		if record.ID <= after {
			return nil, fmt.Errorf("policy list order mismatch")
		}
		result = append(result, record)
		after = record.ID
	}
	return result, nil
}

// Recover 完成已预留的发布。没有 Pending 时读取已发布快照。
func (s *Service) Recover(ctx context.Context, scope Scope, id string) (Release, error) {
	record, token, err := s.get(ctx, scope, id)
	if err != nil {
		return Release{}, err
	}
	return s.finish(ctx, record, token)
}

func (s *Service) finish(ctx context.Context, record Record, token string) (Release, error) {
	if record.Pending == nil {
		return s.GetRelease(ctx, record.Scope, record.ID, record.Published)
	}
	release := *record.Pending
	if release.Scope != record.Scope || release.ID != record.ID || release.Version != record.Revision || release.Version != record.Published+1 {
		return Release{}, fmt.Errorf("invalid pending policy release")
	}
	body, err := json.Marshal(release)
	if err != nil {
		return Release{}, err
	}
	key := releaseKey(record.Scope, record.ID, release.Version)
	err = s.docs.Put(ctx, "releases", key, "", body)
	if errors.Is(err, ErrConflict) {
		saved, _, readErr := s.docs.Get(ctx, "releases", key)
		if readErr != nil {
			return Release{}, readErr
		}
		// 后端可重新排版 JSON；不可变性比较使用无损规范化，不能以 float64 丢失版本精度。
		normalized, normalizeErr := (domain.JSONObject{"saved": saved, "candidate": body}).Normalize()
		if normalizeErr != nil {
			return Release{}, normalizeErr
		}
		if !bytes.Equal(normalized["saved"], normalized["candidate"]) {
			return Release{}, ErrConflict
		}
	} else if err != nil {
		return Release{}, err
	}
	record.Published = release.Version
	record.Compiled = release.Compiled
	record.Spec = release.Spec
	record.Deleted = release.Deleted
	record.Pending = nil
	body, err = json.Marshal(record)
	if err != nil {
		return Release{}, err
	}
	err = s.docs.Put(ctx, "records", record.key(record.ID), token, body)
	if errors.Is(err, ErrConflict) {
		latest, _, readErr := s.get(ctx, record.Scope, record.ID)
		if readErr != nil {
			return Release{}, readErr
		}
		if latest.Published >= release.Version {
			return release, nil
		}
	}
	return release, err
}

// RecoverPage 供控制面有界扫描待发布记录，返回下一页的内部游标；空游标表示扫描一轮结束。
func (s *Service) RecoverPage(ctx context.Context, after string, limit int) (string, error) {
	if limit < 1 || limit > MaxPageSize {
		return after, fmt.Errorf("%w: page size must be 1..16", ErrInvalid)
	}
	rows, err := s.docs.List(ctx, "records", "", after, limit)
	if err != nil {
		return after, err
	}
	next := after
	var failures []error
	for _, raw := range rows {
		var record Record
		if err := json.Unmarshal(raw, &record); err != nil {
			return next, err
		}
		if err := record.Validate(); err != nil {
			return next, err
		}
		if err := validatePolicyID(record.ID); err != nil {
			return next, err
		}
		key := record.key(record.ID)
		if key <= next {
			return next, fmt.Errorf("policy recovery page order mismatch")
		}
		next = key
		if record.Pending != nil {
			if _, err := s.Recover(ctx, record.Scope, record.ID); err != nil {
				failures = append(failures, err)
			}
		}
		if ctx.Err() != nil {
			return next, ctx.Err()
		}
	}
	if len(rows) < limit {
		next = ""
	}
	return next, errors.Join(failures...)
}

// Delete 以当前原始配置发布 tombstone，不删除被运行窗口引用的旧快照。
func (s *Service) Delete(ctx context.Context, scope Scope, id string, expected int64, operationID string) (Release, error) {
	if expected < 1 || expected >= 1<<53 {
		return Release{}, fmt.Errorf("%w: deletion requires positive expected_version", ErrInvalid)
	}
	record, err := s.Get(ctx, scope, id)
	if err != nil {
		return Release{}, err
	}
	// 使用被删除版本的快照，使重试不受后续编辑的 Spec 变化影响。
	spec := record.Spec
	if record.Published > expected {
		release, err := s.GetRelease(ctx, scope, id, expected)
		if err != nil {
			return Release{}, err
		}
		spec = release.Spec
	}
	return s.Apply(ctx, ApplyRequest{Scope: scope, SchemaVersion: 1, ID: id, ExpectedVersion: expected, OperationID: operationID, Spec: spec, Deleted: true})
}
