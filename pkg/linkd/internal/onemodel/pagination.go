// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package onemodel

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var (
	// ErrInvalidQuery 表示查询条件不符合只读查询契约。
	ErrInvalidQuery = errors.New("invalid onemodel query")
	// ErrInvalidCursor 表示游标被修改或不属于当前查询。
	ErrInvalidCursor = errors.New("invalid onemodel cursor")
	// ErrCursorExpired 表示查询快照已过期，需要从第一页重新查询。
	ErrCursorExpired = errors.New("onemodel cursor expired; restart query")
)

// PageQuery 使用独立分页契约，不改变丰富 Search 的完整结果上限。
type PageQuery struct {
	ModelID string `json:"model_id"`
	Where   Filter `json:"where"`
	Limit   int    `json:"limit"`
	Cursor  string `json:"cursor,omitempty"`
}

// Page 包含完整的一页和服务端签名游标；没有游标表示快照已关闭。
type Page struct {
	Instances  []Instance
	NextCursor string
}

// Pager 持有进程级游标签名密钥，不缓存查询数据；可以并发调用。
// 重启使已有签名失效；遗留 PIT 由 ES 的一分钟 keep_alive 回收。
type Pager struct {
	client *Client
	key    [32]byte
	now    func() time.Time
}

type pageCursor struct {
	Version   int               `json:"version"`
	Tenant    string            `json:"tenant"`
	QueryHash string            `json:"query_hash"`
	PIT       string            `json:"pit"`
	After     []json.RawMessage `json:"after"`
	Expires   int64             `json:"expires"`
}

// NewPager 生成仅用于临时游标签名的随机密钥，不参与任何业务身份。
func NewPager(client *Client) (*Pager, error) {
	if client == nil {
		return nil, fmt.Errorf("onemodel client is required")
	}
	p := &Pager{client: client, now: time.Now}
	if _, err := rand.Read(p.key[:]); err != nil {
		return nil, fmt.Errorf("create cursor key: %w", err)
	}
	return p, nil
}

// ValidateQuery 在外部读取前校验租户、模型和类型化过滤。
func ValidateQuery(tenant string, q Query) error {
	if strings.TrimSpace(tenant) == "" || strings.TrimSpace(q.ModelID) == "" {
		return fmt.Errorf("%w: tenant and model_id are required", ErrInvalidQuery)
	}
	if err := validateOneModelIdentity("tenant", tenant, 64); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidQuery, err)
	}
	if err := validateOneModelIdentity("model code", q.ModelID, 128); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidQuery, err)
	}
	if q.Limit < 1 || q.Limit > 1024 {
		return fmt.Errorf("%w: limit must be 1..1024", ErrInvalidQuery)
	}
	if !q.Where.Empty() {
		if _, err := q.Where.Compile(); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidQuery, err)
		}
	}
	return nil
}

// Search 使用固定租户与模型过滤分页，PIT 保证翻页期间视图一致。
// 失败和末页主动关闭 PIT；放弃查询的调用方应调用 Close。
func (p *Pager) Search(ctx context.Context, tenant string, q PageQuery) (page Page, err error) {
	if ctx == nil {
		return Page{}, fmt.Errorf("%w: context is required", ErrInvalidQuery)
	}
	if q.Limit == 0 {
		q.Limit = 50
	}
	if q.Limit > 200 {
		return Page{}, fmt.Errorf("%w: page limit must be 1..200", ErrInvalidQuery)
	}
	if err := ValidateQuery(tenant, Query{ModelID: q.ModelID, Where: q.Where, Limit: q.Limit}); err != nil {
		return Page{}, err
	}
	hashQuery := q
	hashQuery.Cursor = ""
	raw, err := json.Marshal(hashQuery)
	if err != nil {
		return Page{}, fmt.Errorf("%w: invalid filter value", ErrInvalidQuery)
	}
	sum := sha256.Sum256(raw)
	digest := base64.RawURLEncoding.EncodeToString(sum[:])
	cursor := pageCursor{Version: 1, Tenant: tenant, QueryHash: digest}
	if q.Cursor != "" {
		cursor, err = p.decode(q.Cursor)
		if err != nil {
			return Page{}, err
		}
		if cursor.Tenant != tenant || cursor.QueryHash != digest {
			return Page{}, ErrInvalidCursor
		}
	} else {
		var opened struct {
			ID     string `json:"id"`
			Shards struct {
				Failed int `json:"failed"`
			} `json:"_shards"`
		}
		err = p.request(ctx, http.MethodPost, "/"+oneModelInstanceIndex+"/_pit?keep_alive=1m", nil, &opened)
		if opened.ID != "" {
			cursor.PIT = opened.ID
		}
		if err != nil || opened.Shards.Failed > 0 || cursor.PIT == "" {
			if cursor.PIT != "" {
				p.closePIT(ctx, cursor.PIT)
			}
			if err != nil {
				return Page{}, err
			}
			return Page{}, fmt.Errorf("%w: incomplete PIT", ErrInvalidDataSourceResponse)
		}
	}
	// 即使父请求已取消，也用独立且有界的清理上下文释放 ES 快照。
	defer func() {
		if err != nil {
			p.closePIT(ctx, cursor.PIT)
		}
	}()
	filters := []any{term("bk_tenant_id", tenant), term("model_id", q.ModelID)}
	if !q.Where.Empty() {
		clause, _ := q.Where.Compile()
		filters = append(filters, clause)
	}
	body := map[string]any{
		"size": q.Limit + 1, "track_total_hits": false,
		"pit":   map[string]any{"id": cursor.PIT, "keep_alive": "1m"},
		"query": map[string]any{"bool": map[string]any{"filter": filters}},
		// 租户和模型固定，实例身份在物理索引内唯一；_index 区分 alias 的多个索引。
		// 不使用 ES 7.10 尚不支持的 _shard_doc。
		"sort": []any{map[string]string{"model_inst_id": "asc"}, map[string]string{"_index": "asc"}},
	}
	if len(cursor.After) > 0 {
		body["search_after"] = cursor.After
	}
	var response struct {
		PIT      string `json:"pit_id"`
		TimedOut bool   `json:"timed_out"`
		Shards   struct {
			Failed int `json:"failed"`
		} `json:"_shards"`
		Hits struct {
			Hits []struct {
				Source map[string]any    `json:"_source"`
				Sort   []json.RawMessage `json:"sort"`
			} `json:"hits"`
		} `json:"hits"`
	}
	err = p.request(ctx, http.MethodPost, "/_search", body, &response)
	if response.PIT != "" {
		cursor.PIT = response.PIT
	}
	if err != nil {
		return Page{}, err
	}
	if response.TimedOut || response.Shards.Failed > 0 {
		return Page{}, fmt.Errorf("%w: incomplete search", ErrInvalidDataSourceResponse)
	}
	hits := response.Hits.Hits
	if hits == nil {
		return Page{}, fmt.Errorf("%w: missing search hits", ErrInvalidDataSourceResponse)
	}
	// ES 7.12+ 自动追加 PIT 的 _shard_doc；显式实例身份和索引已形成全序，
	// 游标只保留两项显式排序，与仓库既有 ES 7.10 分页兼容策略一致。
	for i := range hits {
		if len(hits[i].Sort) == 3 {
			hits[i].Sort = hits[i].Sort[:2]
		}
	}
	if len(hits) > q.Limit+1 {
		return Page{}, fmt.Errorf("%w: excessive page size", ErrInvalidDataSourceResponse)
	}
	page.Instances = make([]Instance, 0, min(len(hits), q.Limit))
	previous := ""
	if len(cursor.After) > 0 {
		_ = json.Unmarshal(cursor.After[0], &previous)
	}
	for i, hit := range hits {
		instance, _, parseErr := parseInstanceSource(hit.Source, tenant, InstanceQuery{ModelCode: q.ModelID})
		if parseErr != nil {
			return Page{}, parseErr
		}
		var id, index string
		if len(hit.Sort) != 2 || json.Unmarshal(hit.Sort[0], &id) != nil || json.Unmarshal(hit.Sort[1], &index) != nil || id != instance.InstanceID || index == "" || (previous != "" && id <= previous) {
			return Page{}, fmt.Errorf("%w: duplicate identity or invalid page order", ErrInvalidDataSourceResponse)
		}
		previous = id
		if i < q.Limit {
			page.Instances = append(page.Instances, instance)
		}
	}
	if ctx.Err() != nil {
		return Page{}, ctx.Err()
	}
	if len(hits) > q.Limit {
		cursor.After = hits[q.Limit-1].Sort
		cursor.Expires = p.now().Add(time.Minute).Unix()
		page.NextCursor, err = p.encode(cursor)
		if err != nil {
			return Page{}, err
		}
	} else {
		p.closePIT(ctx, cursor.PIT)
	}
	return page, nil
}

// Close 只释放签名有效且属于显式租户的快照；过期游标无需再访问 ES。
func (p *Pager) Close(ctx context.Context, tenant, encoded string) error {
	if ctx == nil {
		return fmt.Errorf("%w: context is required", ErrInvalidQuery)
	}
	cursor, err := p.decode(encoded)
	if errors.Is(err, ErrCursorExpired) {
		return nil
	}
	if err != nil {
		return err
	}
	if tenant == "" || cursor.Tenant != tenant {
		return ErrInvalidCursor
	}
	p.closePIT(ctx, cursor.PIT)
	return nil
}

func (p *Pager) encode(cursor pageCursor) (string, error) {
	raw, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, p.key[:])
	_, _ = mac.Write(raw)
	encoded := base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if len(encoded) > 16384 {
		return "", fmt.Errorf("%w: cursor exceeds limit", ErrInvalidDataSourceResponse)
	}
	return encoded, nil
}

func (p *Pager) decode(encoded string) (pageCursor, error) {
	if len(encoded) > 16384 {
		return pageCursor{}, ErrInvalidCursor
	}
	body, signature, ok := strings.Cut(encoded, ".")
	if !ok {
		return pageCursor{}, ErrInvalidCursor
	}
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return pageCursor{}, ErrInvalidCursor
	}
	sig, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		return pageCursor{}, ErrInvalidCursor
	}
	mac := hmac.New(sha256.New, p.key[:])
	_, _ = mac.Write(raw)
	if !hmac.Equal(mac.Sum(nil), sig) {
		return pageCursor{}, ErrInvalidCursor
	}
	var cursor pageCursor
	if json.Unmarshal(raw, &cursor) != nil || cursor.Version != 1 || cursor.PIT == "" || cursor.Tenant == "" || len(cursor.After) != 2 {
		return pageCursor{}, ErrInvalidCursor
	}
	if p.now().Unix() >= cursor.Expires {
		return pageCursor{}, ErrCursorExpired
	}
	return cursor, nil
}

func (p *Pager) closePIT(ctx context.Context, id string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	// 清理失败不覆盖成功结果或原始错误；ES 会在固定 keep_alive 后释放快照。
	_ = p.request(ctx, http.MethodDelete, "/_pit", map[string]string{"id": id}, nil)
}

func (p *Pager) request(ctx context.Context, method, path string, body, result any) error {
	var payload []byte
	var err error
	if body != nil {
		payload, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	request, err := http.NewRequestWithContext(ctx, method, path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := p.client.transport.Perform(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusNotFound && path == "/_search" {
		return ErrCursorExpired
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("onemodel backend HTTP %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxOneModelResponseBytes+1))
	if err != nil {
		return err
	}
	if len(raw) > maxOneModelResponseBytes {
		return fmt.Errorf("%w: response exceeds limit", ErrInvalidDataSourceResponse)
	}
	if result == nil {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(result); err != nil {
		return fmt.Errorf("%w: invalid backend response", ErrInvalidDataSourceResponse)
	}
	return nil
}
