// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package model

type ResourceEvent struct {
	CommonFields
	Resource Resource `json:"resource"`
}

func (e *ResourceEvent) GetType() EventType { return EventTypeResource }
func (e *ResourceEvent) GetCommon() *CommonFields {
	return &e.CommonFields
}

type Resource struct {
	Present              bool              `json:"-"`
	ID                   string            `json:"id,omitempty"`
	Type                 string            `json:"type,omitempty"`
	URL                  string            `json:"url,omitempty"`
	URLHost              string            `json:"url_host,omitempty"`
	URLPath              string            `json:"url_path,omitempty"`
	URLQuery             string            `json:"url_query,omitempty"`
	URLScheme            string            `json:"url_scheme,omitempty"`
	Method               string            `json:"method,omitempty"`
	StatusCode           *int              `json:"status_code,omitempty"`
	Duration             *int64            `json:"duration,omitempty"`
	Size                 *int64            `json:"size,omitempty"`
	EncodedBodySize      *int64            `json:"encoded_body_size,omitempty"`
	DecodedBodySize      *int64            `json:"decoded_body_size,omitempty"`
	TransferSize         *int64            `json:"transfer_size,omitempty"`
	Protocol             string            `json:"protocol,omitempty"`
	DeliveryType         string            `json:"delivery_type,omitempty"`
	RenderBlockingStatus string            `json:"render_blocking_status,omitempty"`
	Timing               *ResourceTiming   `json:"timing,omitempty"`
	Provider             *ResourceProvider `json:"provider,omitempty"`
	Request              map[string]any    `json:"request,omitempty"`
	Response             map[string]any    `json:"response,omitempty"`
}

type ResourceTiming struct {
	Redirect  *Timing `json:"redirect,omitempty"`
	Worker    *Timing `json:"worker,omitempty"`
	DNS       *Timing `json:"dns,omitempty"`
	Connect   *Timing `json:"connect,omitempty"`
	SSL       *Timing `json:"ssl,omitempty"`
	FirstByte *Timing `json:"first_byte,omitempty"`
	Download  *Timing `json:"download,omitempty"`
}

type ResourceProvider struct {
	Type   string `json:"type,omitempty"`
	Name   string `json:"name,omitempty"`
	Domain string `json:"domain,omitempty"`
}

type Timing struct {
	Start    *int64 `json:"start,omitempty"`
	Duration *int64 `json:"duration,omitempty"`
}
