// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package model

type User struct {
	ID          string `json:"id,omitempty"`
	Name        string `json:"name,omitempty"`
	Email       string `json:"email,omitempty"`
	AnonymousID string `json:"anonymous_id,omitempty"`
}

type Account struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

type Tab struct {
	ID string `json:"id,omitempty"`
}

type Connectivity struct {
	Status        string           `json:"status,omitempty"`
	EffectiveType string           `json:"effective_type,omitempty"`
	Interfaces    []string         `json:"interfaces,omitempty"`
	Cellular      *CellularNetwork `json:"cellular,omitempty"`
}

type CellularNetwork struct {
	Technology  string `json:"technology,omitempty"`
	CarrierName string `json:"carrier_name,omitempty"`
}

type Display struct {
	Viewport *Viewport      `json:"viewport,omitempty"`
	Scroll   *DisplayScroll `json:"scroll,omitempty"`
}

type Viewport struct {
	Width  *int64 `json:"width,omitempty"`
	Height *int64 `json:"height,omitempty"`
}

type DisplayScroll struct {
	MaxDepth            *int64 `json:"max_depth,omitempty"`
	MaxDepthScrollTop   *int64 `json:"max_depth_scroll_top,omitempty"`
	MaxScrollHeight     *int64 `json:"max_scroll_height,omitempty"`
	MaxScrollHeightTime *int64 `json:"max_scroll_height_time,omitempty"`
}

type Device struct {
	Type            string   `json:"type,omitempty"`
	Name            string   `json:"name,omitempty"`
	Brand           string   `json:"brand,omitempty"`
	Model           string   `json:"model,omitempty"`
	Architecture    string   `json:"architecture,omitempty"`
	Locale          string   `json:"locale,omitempty"`
	Locales         []string `json:"locales,omitempty"`
	TimeZone        string   `json:"time_zone,omitempty"`
	BatteryLevel    *float64 `json:"battery_level,omitempty"`
	PowerSavingMode *bool    `json:"power_saving_mode,omitempty"`
	BrightnessLevel *float64 `json:"brightness_level,omitempty"`
	LogicalCPUCount *int64   `json:"logical_cpu_count,omitempty"`
	TotalRAM        *int64   `json:"total_ram,omitempty"`
	IsLowRAM        *bool    `json:"is_low_ram,omitempty"`
}

type OS struct {
	Name         string `json:"name,omitempty"`
	Version      string `json:"version,omitempty"`
	VersionMajor string `json:"version_major,omitempty"`
	Build        string `json:"build,omitempty"`
}
