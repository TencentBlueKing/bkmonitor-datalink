// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package bkapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

var userAPIClient = &http.Client{
	Timeout: 5 * time.Second,
	// A redirect must not forward application credentials to another endpoint.
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func userAPIBaseURL(apiAddress, dataURI string) string {
	if base := strings.TrimSpace(os.Getenv("BKAPP_USER_API_BASE_URL")); base != "" {
		return base
	}
	if root := strings.TrimSpace(os.Getenv("BK_COMPONENT_API_URL")); root != "" {
		return strings.TrimRight(root, "/") + "/api/bk-user/prod/"
	}
	// Older deployments describe the shared gateway as bk_api.address plus a
	// path routed under /api/bk-base/. A direct BKData address cannot identify BKUser.
	gateway, err := url.Parse(apiAddress)
	if err != nil || gateway.Host == "" || (gateway.Scheme != "http" && gateway.Scheme != "https") {
		return ""
	}
	route := "/" + strings.TrimLeft(gateway.JoinPath(dataURI).Path, "/")
	root, _, shared := strings.Cut(route, "/api/bk-base/")
	if !shared {
		return ""
	}
	return (&url.URL{Scheme: gateway.Scheme, Host: gateway.Host, Path: root + "/api/bk-user/prod/"}).String()
}

func (i *BkDataAPI) tenantAdmin(ctx context.Context, tenantID string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if tenantID == "" {
		return "", errors.New("BKUser admin lookup requires a tenant ID")
	}
	if username, ok := i.adminUsers.Load(tenantID); ok {
		return username.(string), nil
	}
	lookup := i.adminLookups.DoChan(tenantID, func() (any, error) {
		if username, ok := i.adminUsers.Load(tenantID); ok {
			return username, nil
		}
		// Coalesced lookups have their own HTTP timeout. Each caller can cancel its wait.
		username, err := i.lookupAdmin(context.WithoutCancel(ctx), tenantID)
		if err == nil {
			i.adminUsers.Store(tenantID, username)
		}
		return username, err
	})
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case result := <-lookup:
		if result.Err != nil {
			return "", result.Err
		}
		return result.Val.(string), nil
	}
}

func (i *BkDataAPI) lookupAdmin(ctx context.Context, tenantID string) (string, error) {
	if i.userAPIURL == "" {
		return "", errors.New("BKUser admin lookup requires BKAPP_USER_API_BASE_URL or BK_COMPONENT_API_URL")
	}
	endpoint, err := url.Parse(i.userAPIURL)
	if err != nil {
		return "", errors.New("invalid BKUser base URL")
	}
	endpoint = endpoint.JoinPath("api/v3/open/tenant/virtual-users/-/lookup/")
	login := i.adminLogin
	if login == "" {
		login = "bk_admin"
	}
	endpoint.RawQuery = url.Values{"lookup_field": {"login_name"}, "lookups": {login}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return "", fmt.Errorf("create BKUser admin lookup: %w", err)
	}
	auth, _ := json.Marshal(map[string]string{
		BkAppCodeKey: i.bkAPI.GetCode(), BkSecretKey: i.bkAPI.authConfig[BkSecretKey],
	})
	req.Header.Set(BkAPIAuthorization, string(auth))
	req.Header.Set("X-Bk-Tenant-Id", tenantID)
	resp, err := userAPIClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("BKUser admin lookup: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("BKUser admin lookup returned HTTP %d", resp.StatusCode)
	}
	var result struct {
		Code   int   `json:"code"`
		Result *bool `json:"result"`
		Data   []struct {
			Username string `json:"bk_username"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode BKUser admin lookup: %w", err)
	}
	if result.Code != 0 || (result.Result != nil && !*result.Result) {
		return "", fmt.Errorf("BKUser admin lookup failed (code %d)", result.Code)
	}
	if len(result.Data) != 1 || strings.TrimSpace(result.Data[0].Username) == "" {
		return "", errors.New("BKUser admin lookup did not return one username")
	}
	return result.Data[0].Username, nil
}
