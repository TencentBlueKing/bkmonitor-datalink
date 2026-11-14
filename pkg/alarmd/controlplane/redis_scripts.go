// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane

import "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/redisscript"

// The scripts this package defines as constants, by the names their cost reads
// under (redisscript).
//
// renewSnapshotPublicationScript and restoreSnapshotPublicationScript are one
// text, so they are one name and their calls read together.
var (
	_ = redisscript.Register("controlplane_cas_initial_schedules", compareAndSetInitialSchedulesScript)
	_ = redisscript.Register("controlplane_cas_cutover_schedules", compareAndSetCutoverSchedulesScript)
	_ = redisscript.Register("controlplane_publish_snapshot", publishSnapshotScript)
	_ = redisscript.Register("controlplane_publish_audit", publishAuditScript)
	_ = redisscript.Register("controlplane_renew_current_activation_objects", renewCurrentActivationObjectsScript)
	_ = redisscript.Register("controlplane_refresh_snapshot_publication", renewSnapshotPublicationScript)
	_ = redisscript.Register("controlplane_refresh_snapshot_publication", restoreSnapshotPublicationScript)
	_ = redisscript.Register("controlplane_rebuild_activation_header", rebuildActivationHeaderScript)
	_ = redisscript.Register("controlplane_rebuild_activation_body", rebuildActivationBodyScript)
	_ = redisscript.Register("controlplane_write_catalog_manifest", writeCatalogManifestScript)
)
