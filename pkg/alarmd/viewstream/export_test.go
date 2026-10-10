// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package viewstream

import "time"

// BackoffsForTest samples the reconnect wait for one attempt.
func BackoffsForTest(client *Client, attempt, samples int) []time.Duration {
	waits := make([]time.Duration, 0, samples)
	for i := 0; i < samples; i++ {
		waits = append(waits, client.backoff(attempt))
	}
	return waits
}
