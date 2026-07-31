// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package model

type ViewEvent struct {
	CommonFields
	View View `json:"view"`
}

// GetType returns the explicit event type when present; otherwise it defaults to EventTypeView.
// View events may carry a more specific subtype in CommonFields.Type, unlike other event types.
func (e *ViewEvent) GetType() EventType {
	if e.CommonFields.Type == "" {
		return EventTypeView
	}
	return e.CommonFields.Type
}
func (e *ViewEvent) GetCommon() *CommonFields {
	return &e.CommonFields
}

type View struct {
	Present bool `json:"-"`
	ViewContext
	IsActive                             *bool            `json:"is_active,omitempty"`
	LoadingType                          string           `json:"loading_type,omitempty"`
	LoadingTime                          *int64           `json:"loading_time,omitempty"`
	NetworkSettledTime                   *int64           `json:"network_settled_time,omitempty"`
	InteractionToNextViewTime            *int64           `json:"interaction_to_next_view_time,omitempty"`
	TimeSpent                            *int64           `json:"time_spent,omitempty"`
	FirstContentfulPaint                 *int64           `json:"first_contentful_paint,omitempty"`
	LargestContentfulPaint               *int64           `json:"largest_contentful_paint,omitempty"`
	LargestContentfulPaintTargetSelector string           `json:"largest_contentful_paint_target_selector,omitempty"`
	FirstInputDelay                      *int64           `json:"first_input_delay,omitempty"`
	FirstInputTime                       *int64           `json:"first_input_time,omitempty"`
	FirstInputTargetSelector             string           `json:"first_input_target_selector,omitempty"`
	InteractionToNextPaint               *int64           `json:"interaction_to_next_paint,omitempty"`
	InteractionToNextPaintTime           *int64           `json:"interaction_to_next_paint_time,omitempty"`
	InteractionToNextPaintTargetSelector string           `json:"interaction_to_next_paint_target_selector,omitempty"`
	CumulativeLayoutShift                *float64         `json:"cumulative_layout_shift,omitempty"`
	CumulativeLayoutShiftTime            *int64           `json:"cumulative_layout_shift_time,omitempty"`
	CumulativeLayoutShiftTargetSelector  string           `json:"cumulative_layout_shift_target_selector,omitempty"`
	Performance                          *ViewPerformance `json:"performance,omitempty"`
	EventCount                           *ViewEventCount  `json:"event,omitempty"`
	CustomTimings                        CustomTimings    `json:"custom_timings,omitempty"`
	PageState                            *PageState       `json:"page_state,omitempty"`
	Action                               *ViewCount       `json:"action,omitempty"`
	Error                                *ViewCount       `json:"error,omitempty"`
	Crash                                *ViewCount       `json:"crash,omitempty"`
	Resource                             *ViewCount       `json:"resource,omitempty"`
	LongTask                             *ViewCount       `json:"long_task,omitempty"`
	Frustration                          *ViewCount       `json:"frustration,omitempty"`
	InForegroundPeriods                  []Period         `json:"in_foreground_periods,omitempty"`
}

type ViewContext struct {
	ID       string `json:"id,omitempty"`
	Name     string `json:"name,omitempty"`
	URL      string `json:"url,omitempty"`
	Referrer string `json:"referrer,omitempty"`
}

type ViewPerformance struct {
	NavigationStart  *int64          `json:"navigation_start,omitempty"`
	FirstByte        *int64          `json:"first_byte,omitempty"`
	DOMInteractive   *int64          `json:"dom_interactive,omitempty"`
	DOMContentLoaded *int64          `json:"dom_content_loaded,omitempty"`
	DOMComplete      *int64          `json:"dom_complete,omitempty"`
	LoadEvent        *int64          `json:"load_event,omitempty"`
	FCP              *FCPPerformance `json:"fcp,omitempty"`
	LCP              *LCPPerformance `json:"lcp,omitempty"`
	CLS              *CLSPerformance `json:"cls,omitempty"`
	FID              *FIDPerformance `json:"fid,omitempty"`
	INP              *INPPerformance `json:"inp,omitempty"`
}

type FCPPerformance struct {
	Timestamp *int64 `json:"timestamp,omitempty"`
}

type LCPPerformance struct {
	Timestamp      *int64       `json:"timestamp,omitempty"`
	Target         string       `json:"target,omitempty"`
	TargetSelector string       `json:"target_selector,omitempty"`
	ResourceURL    string       `json:"resource_url,omitempty"`
	Element        string       `json:"element,omitempty"`
	SubParts       *LCPSubParts `json:"sub_parts,omitempty"`
}

type LCPSubParts struct {
	LoadDelay   *int64 `json:"load_delay,omitempty"`
	LoadTime    *int64 `json:"load_time,omitempty"`
	RenderDelay *int64 `json:"render_delay,omitempty"`
}

type CLSPerformance struct {
	Score          *float64 `json:"score,omitempty"`
	Value          *float64 `json:"value,omitempty"`
	Timestamp      *int64   `json:"timestamp,omitempty"`
	TargetSelector string   `json:"target_selector,omitempty"`
	PreviousRect   *Rect    `json:"previous_rect,omitempty"`
	CurrentRect    *Rect    `json:"current_rect,omitempty"`
}

type FIDPerformance struct {
	Duration       *int64 `json:"duration,omitempty"`
	Timestamp      *int64 `json:"timestamp,omitempty"`
	TargetSelector string `json:"target_selector,omitempty"`
}

type INPPerformance struct {
	Duration       *int64       `json:"duration,omitempty"`
	Value          *float64     `json:"value,omitempty"`
	Timestamp      *int64       `json:"timestamp,omitempty"`
	TargetSelector string       `json:"target_selector,omitempty"`
	Target         string       `json:"target,omitempty"`
	SubParts       *INPSubParts `json:"sub_parts,omitempty"`
}

type INPSubParts struct {
	InputDelay         *int64 `json:"input_delay,omitempty"`
	ProcessingDuration *int64 `json:"processing_duration,omitempty"`
	PresentationDelay  *int64 `json:"presentation_delay,omitempty"`
}

type Rect struct {
	X      *float64 `json:"x,omitempty"`
	Y      *float64 `json:"y,omitempty"`
	Width  *float64 `json:"width,omitempty"`
	Height *float64 `json:"height,omitempty"`
}

type ViewCount struct {
	Count *int64 `json:"count,omitempty"`
}

type ViewEventCount struct {
	Action   *int64 `json:"action_count,omitempty"`
	Error    *int64 `json:"error_count,omitempty"`
	Resource *int64 `json:"resource_count,omitempty"`
	LongTask *int64 `json:"long_task_count,omitempty"`
}

type CustomTimings map[string]int64

type Period struct {
	Start    *int64 `json:"start,omitempty"`
	Duration *int64 `json:"duration,omitempty"`
	End      *int64 `json:"end,omitempty"`
}

type PageState struct {
	State    string `json:"state,omitempty"`
	Start    *int64 `json:"start,omitempty"`
	Duration *int64 `json:"duration,omitempty"`
}
