// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package beater

import (
	"context"
	"sync"
	"testing"

	"github.com/elastic/beats/libbeat/common"
	"github.com/elastic/go-ucfg"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs/validator"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tenant"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/host"
)

var (
	registerRegexpValidatorOnce sync.Once
	registerRegexpValidatorErr  error
)

func TestReloadRestoresTenantStorageOnParseFailure(t *testing.T) {
	registerRegexpValidatorOnce.Do(func() {
		registerRegexpValidatorErr = ucfg.RegisterValidator("regexp", validator.ValidateRegex)
	})
	if registerRegexpValidatorErr != nil {
		t.Fatalf("register regexp validator: %v", registerRegexpValidatorErr)
	}

	storage := tenant.DefaultStorage()
	storage.SetExpectedTasks([]string{define.ModuleBasereport})
	storage.UpdateTaskDataIDs(map[string]int32{define.ModuleBasereport: 2001})
	t.Cleanup(func() { storage.SetExpectedTasks(nil) })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	state := newBeaterState()
	state.ctx = ctx
	state.cancelFunc = cancel
	monitorBeater := &MonitorBeater{
		beaterState:   state,
		beaterStatus:  newBeaterStatus(),
		hostIDWatcher: host.NewEmptyWatcher(),
		configEngine:  NewBaseConfigEngine(ctx),
	}

	invalidConfig, err := common.NewConfigFrom(map[string]interface{}{
		"enable_multi_tenant": false,
		"mode":                "daemon",
		"node_id":             "local-test",
		"ip":                  "localhost",
		"bk_biz_id":           1,
		"bk_cloud_id":         0,
	})
	if err != nil {
		t.Fatalf("create invalid reload config: %v", err)
	}
	monitorBeater.Reload(invalidConfig)

	if got := storage.ResolveTaskDataID(define.ModuleBasereport, 1001); got != 2001 {
		t.Fatalf("basereport data ID after failed reload = %d, want 2001", got)
	}
	if updated := storage.UpdateTaskDataIDs(map[string]int32{define.ModuleBasereport: 2001}); !updated {
		t.Fatal("expected failed reload to keep tenant data ID pending for retry")
	}
}
