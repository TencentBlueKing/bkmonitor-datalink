// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package api

import (
	"context"
	"errors"
	"net/http"

	"linkd/internal/lifecycle"
	lifecycleprocess "linkd/internal/lifecycle/process"
	"linkd/internal/policy"
	"linkd/internal/store"
)

// ManualShieldBinder 执行显式快捷屏蔽命令，不开放任意 Alert 字段写入。
type ManualShieldBinder interface {
	BindShield(context.Context, lifecycle.ShieldCommand) (lifecycle.ShieldCommandResult, error)
}

func (a *API) bindManualShield(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if a.ManualShieldBinder == nil {
		policyFailure(w, policy.ErrUnavailable)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var command lifecycle.ShieldCommand
	if r.URL.RawQuery != "" || decode(r, &command) != nil || command.AlertID != "" {
		policyFailure(w, policy.ErrInvalid)
		return
	}
	command.AlertID = r.PathValue("id")
	if command.Validate() != nil {
		policyFailure(w, policy.ErrInvalid)
		return
	}
	result, err := a.ManualShieldBinder.BindShield(r.Context(), command)
	if err != nil {
		var classified *lifecycleprocess.CloseError
		switch {
		case errors.As(err, &classified):
			http.Error(w, classified.Message, classified.Status)
		case errors.Is(err, store.ErrInvalidArgument):
			policyFailure(w, policy.ErrInvalid)
		case errors.Is(err, store.ErrInvalidTransition), errors.Is(err, store.ErrVersionConflict):
			policyFailure(w, policy.ErrConflict)
		case errors.Is(err, store.ErrNotFound):
			policyFailure(w, policy.ErrNotFound)
		default:
			shieldFailure(w, err)
		}
		return
	}
	output(w, result)
}
