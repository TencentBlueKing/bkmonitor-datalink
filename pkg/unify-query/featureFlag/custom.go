// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package featureFlag

import (
	"context"
	"log"

	"github.com/thomaspoignant/go-feature-flag/exporter"
)

// CustomExport
type CustomExport struct{}

// Export
func (e *CustomExport) Export(ctx context.Context, _ *log.Logger, featureEvents []exporter.FeatureEvent) error {
	return setEvent(ctx, featureEvents)
}

// IsBulk
func (e *CustomExport) IsBulk() bool {
	return false
}

// CustomRetriever
type CustomRetriever struct{}

// Retrieve
func (s *CustomRetriever) Retrieve(_ context.Context) ([]byte, error) {
	return getFeatureFlags(), nil
}
