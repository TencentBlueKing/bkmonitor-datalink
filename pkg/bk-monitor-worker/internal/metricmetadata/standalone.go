// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT

package metricmetadata

import "context"

// RunStandalone consumes the DataID configured at the metricmetadata Consul key until ctx is cancelled.
// The caller initializes configuration and logging and owns the context lifecycle.
func RunStandalone(ctx context.Context) error {
	return Run(ctx, 0)
}
