// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lifecycleprocess

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/store"
	"linkd/internal/taskdispatch"
)

// configuredAlertProjector 复用控制面的兼容写入器和就绪状态；Worker 不维护第二套 KAC 索引。
type configuredAlertProjector struct{ dispatch config.DispatchConfig }

func (p configuredAlertProjector) Project(ctx context.Context, row store.StoredAlert) (store.StoredAlert, error) {
	call, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var response struct {
		Alert    domain.Alert `json:"alert"`
		Version  string       `json:"version"`
		Deferred bool         `json:"deferred"`
	}
	client := taskdispatch.Client{URL: p.dispatch.WithDefaults().URL, Token: p.dispatch.WorkerToken}
	err := client.Call(call, http.MethodPost, "/internal/alerts/"+url.PathEscape(row.Alert.AlertID)+"/projection", map[string]string{"bk_tenant_id": row.Alert.BKTenantID}, &response)
	if err != nil {
		return row, err
	}
	if response.Deferred {
		return row, fmt.Errorf("KAC projection unconfirmed")
	}
	return store.StoredAlert{Alert: response.Alert, Version: store.NewVersionToken(response.Version)}, nil
}
