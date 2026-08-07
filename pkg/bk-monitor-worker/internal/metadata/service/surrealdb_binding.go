// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/tenant"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	graphRelationBindingLabelKey    = "bkm_data_link_strategy"
	graphRelationBindingLabelValue  = "graph_relation_time_series"
	surrealDBBindingRedisCoreKey    = "surrealdb_binding"
	surrealDBBindingRedisChannelKey = "surrealdb_binding:channel"
)

var errSurrealDBBindingSourceNotConfigured = errors.New("surrealdb binding resource api url is not configured")

type SurrealDBBindingDetail struct {
	Name        string `json:"name"`
	BkBizID     string `json:"bk_biz_id"`
	Database    string `json:"database"`
	Namespace   string `json:"namespace"`
	ClusterName string `json:"cluster_name"`
	Phase       string `json:"phase"`
}

type SurrealDBBindingSource interface {
	ListSurrealDBBindings(ctx context.Context, bkTenantId string) ([]SurrealDBBindingDetail, error)
}

type BKBaseSurrealDBBindingSource struct {
	baseURL    string
	httpClient *http.Client
}

func NewBKBaseSurrealDBBindingSource() *BKBaseSurrealDBBindingSource {
	return &BKBaseSurrealDBBindingSource{
		baseURL: cfg.SurrealDBBindingResourceAPIURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (s *BKBaseSurrealDBBindingSource) ListSurrealDBBindings(ctx context.Context, bkTenantId string) ([]SurrealDBBindingDetail, error) {
	baseURL := strings.TrimRight(s.baseURL, "/")
	if baseURL == "" {
		return nil, errSurrealDBBindingSourceNotConfigured
	}
	if !strings.HasSuffix(baseURL, "/surrealdbbindings") {
		baseURL = fmt.Sprintf("%s/surrealdbbindings", baseURL)
	}
	query := url.Values{}
	query.Set("label_selector", fmt.Sprintf("%s=%s", graphRelationBindingLabelKey, graphRelationBindingLabelValue))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/?%s", baseURL, query.Encode()), nil)
	if err != nil {
		return nil, err
	}
	adminUser, err := tenant.GetTenantAdminUser(bkTenantId)
	if err != nil {
		return nil, err
	}

	bkapiAuth, err := json.Marshal(map[string]string{
		"bk_app_code":   cfg.BkApiAppCode,
		"bk_app_secret": cfg.BkApiAppSecret,
		"bk_username":   adminUser,
	})
	if err != nil {
		return nil, err
	}
	bkbaseAuth, err := json.Marshal(map[string]string{
		"bk_app_code":                  cfg.BkApiAppCode,
		"bk_username":                  adminUser,
		"bkdata_authentication_method": "user",
		"bkdata_data_token":            "",
	})
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Bkapi-Authorization", string(bkapiAuth))
	req.Header.Set("X-Bkbase-Authorization", string(bkbaseAuth))
	if bkTenantId != "" {
		req.Header.Set("X-Bk-Tenant-Id", bkTenantId)
	}

	client := s.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("list SurrealDBBinding failed: status=%s", resp.Status)
	}

	var listResp bkbaseSurrealDBBindingListResponse
	if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
		return nil, err
	}
	if !listResp.Result {
		return nil, fmt.Errorf("bkbase list SurrealDBBinding response error: code=%s, message=%s", listResp.Code, listResp.Message)
	}

	return selectSurrealDBBindingDetails(listResp.Data)
}

type bkbaseSurrealDBBindingListResponse struct {
	Result  bool                     `json:"result"`
	Code    string                   `json:"code"`
	Message string                   `json:"message"`
	Data    []bkbaseSurrealDBBinding `json:"data"`
}

type bkbaseSurrealDBBinding struct {
	Metadata struct {
		Name        string            `json:"name"`
		Labels      map[string]string `json:"labels"`
		Annotations map[string]string `json:"annotations"`
	} `json:"metadata"`
	Spec struct {
		Storage struct {
			Name string `json:"name"`
		} `json:"storage"`
	} `json:"spec"`
	Status struct {
		Phase string `json:"phase"`
	} `json:"status"`
}

func selectSurrealDBBindingDetails(items []bkbaseSurrealDBBinding) ([]SurrealDBBindingDetail, error) {
	detailByBiz := make(map[string]SurrealDBBindingDetail)
	for _, item := range items {
		if item.Metadata.Labels[graphRelationBindingLabelKey] != graphRelationBindingLabelValue {
			continue
		}
		if item.Status.Phase != "Ok" {
			continue
		}
		bizID := item.Metadata.Labels["bk_biz_id"]
		database := item.Metadata.Annotations["database"]
		namespace := item.Metadata.Annotations["namespace"]
		if bizID == "" || database == "" || namespace == "" {
			continue
		}
		if _, ok := detailByBiz[bizID]; ok {
			return nil, fmt.Errorf("multiple preferred SurrealDBBinding found for bk_biz_id=%s", bizID)
		}

		detailByBiz[bizID] = SurrealDBBindingDetail{
			Name:        item.Metadata.Name,
			BkBizID:     bizID,
			Database:    database,
			Namespace:   namespace,
			ClusterName: item.Spec.Storage.Name,
			Phase:       item.Status.Phase,
		}
	}

	details := make([]SurrealDBBindingDetail, 0, len(detailByBiz))
	for _, detail := range detailByBiz {
		details = append(details, detail)
	}

	return details, nil
}

func (s *SpacePusher) PushSurrealDBBindings(ctx context.Context, bkTenantId string, isPublish bool) error {
	source := NewBKBaseSurrealDBBindingSource()
	details, err := source.ListSurrealDBBindings(ctx, bkTenantId)
	if errors.Is(err, errSurrealDBBindingSourceNotConfigured) {
		logger.Infof("PushSurrealDBBindings: skipped, %s", err.Error())
		return nil
	}
	if err != nil {
		return err
	}
	return s.PushSurrealDBBindingDetails(bkTenantId, details, isPublish)
}

func (s *SpacePusher) PushSurrealDBBindingDetails(bkTenantId string, details []SurrealDBBindingDetail, isPublish bool) error {
	client := redis.GetStorageRedisInstance()
	key := surrealDBBindingRedisKey()
	channel := surrealDBBindingRedisChannel()
	activeFields := make(map[string]struct{}, len(details))
	for _, detail := range details {
		if detail.BkBizID == "" || detail.Database == "" || detail.Namespace == "" {
			return fmt.Errorf("invalid SurrealDBBinding route detail: %+v", detail)
		}

		field := composeTenantRedisKey(SpaceRouteKey(models.SpaceTypeBKCC, detail.BkBizID), bkTenantId)
		activeFields[field] = struct{}{}
		value, err := jsonx.MarshalString(detail)
		if err != nil {
			return err
		}

		if !isPublish {
			if err := client.HSet(key, field, value); err != nil {
				return err
			}
			continue
		}

		if _, err := client.HSetWithCompareAndPublish(key, field, value, channel, field); err != nil {
			return err
		}
	}

	return clearStaleSurrealDBBindingDetails(client, key, channel, bkTenantId, activeFields, isPublish)
}

func clearStaleSurrealDBBindingDetails(
	client *redis.Instance,
	key string,
	channel string,
	bkTenantId string,
	activeFields map[string]struct{},
	isPublish bool,
) error {
	fields, err := client.HKeys(key)
	if err != nil {
		return err
	}

	staleFields := make([]string, 0)
	for _, field := range fields {
		if _, ok := activeFields[field]; ok {
			continue
		}
		if !surrealDBBindingFieldBelongsToTenant(field, bkTenantId) {
			continue
		}
		staleFields = append(staleFields, field)
	}
	if len(staleFields) == 0 {
		logger.Infof("clearStaleSurrealDBBindingDetails: no stale SurrealDBBinding route, bk_tenant_id [%s]", bkTenantId)
		return nil
	}
	sort.Strings(staleFields)

	logger.Infof("clearStaleSurrealDBBindingDetails: delete stale SurrealDBBinding routes, bk_tenant_id [%s], fields [%v]", bkTenantId, staleFields)
	if err := client.HDel(key, staleFields...); err != nil {
		return err
	}
	if !isPublish {
		return nil
	}
	for _, field := range staleFields {
		if err := client.Publish(channel, field); err != nil {
			return err
		}
	}
	return nil
}

func surrealDBBindingFieldBelongsToTenant(field string, bkTenantId string) bool {
	if !cfg.EnableMultiTenantMode {
		return true
	}
	return strings.HasSuffix(field, fmt.Sprintf("|%s", bkTenantId))
}

func surrealDBBindingRedisKey() string {
	if cfg.SurrealDBBindingKey != "" {
		return cfg.SurrealDBBindingKey
	}
	return fmt.Sprintf("bkmonitorv3:spaces:%s", surrealDBBindingRedisCoreKey)
}

func surrealDBBindingRedisChannel() string {
	if cfg.SurrealDBBindingChannel != "" {
		return cfg.SurrealDBBindingChannel
	}
	return fmt.Sprintf("bkmonitorv3:spaces:%s", surrealDBBindingRedisChannelKey)
}
