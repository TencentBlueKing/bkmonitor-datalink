// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BK-Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package remote

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/prometheus/prometheus/prompb"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/customreport"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
)

const relationDataIDTokenCacheDuration = 5 * time.Minute

type prometheusWriteClient interface {
	Close(context.Context) error
	WriteBatch(context.Context, string, prompb.WriteRequest) error
}

type relationDataIDTokenProvider func(dataID uint) (string, error)

// RelationDataIDReporter writes relation metrics to one explicitly configured DataID.
// It intentionally does not use the source business or namespace to choose a target.
type RelationDataIDReporter interface {
	Write(ctx context.Context, tsList ...prompb.TimeSeries) error
	Close(ctx context.Context) error
}

type relationDataIDReporter struct {
	dataID        uint
	writer        prometheusWriteClient
	tokenProvider relationDataIDTokenProvider

	mu           sync.Mutex
	token        string
	tokenExpires time.Time
}

// NewRelationDataIDReporter creates a writer for one relation DataID configured by Helm.
func NewRelationDataIDReporter(dataID uint, writerURL string, headers map[string]string) (RelationDataIDReporter, error) {
	if dataID == 0 {
		return nil, fmt.Errorf("relation data ID must be configured")
	}

	return newRelationDataIDReporter(
		dataID,
		NewPrometheusWriterClient("", writerURL, headers),
		getRelationDataIDToken,
	), nil
}

func newRelationDataIDReporter(
	dataID uint,
	writer prometheusWriteClient,
	tokenProvider relationDataIDTokenProvider,
) *relationDataIDReporter {
	return &relationDataIDReporter{
		dataID:        dataID,
		writer:        writer,
		tokenProvider: tokenProvider,
	}
}

func (r *relationDataIDReporter) Close(ctx context.Context) error {
	return r.writer.Close(ctx)
}

func (r *relationDataIDReporter) Write(ctx context.Context, tsList ...prompb.TimeSeries) error {
	if len(tsList) == 0 {
		return nil
	}

	token, err := r.getToken()
	if err != nil {
		return err
	}

	return r.writer.WriteBatch(ctx, token, prompb.WriteRequest{Timeseries: tsList})
}

func (r *relationDataIDReporter) getToken() (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.token != "" && time.Now().Before(r.tokenExpires) {
		return r.token, nil
	}

	// Tokens are stored by Metadata and may rotate; refresh periodically instead of deriving one from DataID.
	token, err := r.tokenProvider(r.dataID)
	if err != nil {
		return "", err
	}
	r.token = token
	r.tokenExpires = time.Now().Add(relationDataIDTokenCacheDuration)
	return token, nil
}

func getRelationDataIDToken(dataID uint) (string, error) {
	var groups []customreport.TimeSeriesGroup
	if err := customreport.NewTimeSeriesGroupQuerySet(mysql.GetDBSession().DB).
		BkDataIDEq(dataID).
		IsEnableEq(true).
		IsDeleteEq(false).
		All(&groups); err != nil {
		return "", fmt.Errorf("query relation DataID %d token: %w", dataID, err)
	}

	if len(groups) != 1 {
		return "", fmt.Errorf("relation DataID %d has %d active time series groups, expected 1", dataID, len(groups))
	}
	if groups[0].Token == "" {
		return "", fmt.Errorf("relation DataID %d token is empty", dataID)
	}

	return groups[0].Token, nil
}
