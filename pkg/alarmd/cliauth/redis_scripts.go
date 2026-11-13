// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cliauth

import "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/redisscript"

// The scripts this package defines as constants, by the names their cost reads
// under (redisscript).
var (
	_ = redisscript.Register("cliauth_issue", issueScript)
	_ = redisscript.Register("cliauth_exchange", exchangeScript)
	_ = redisscript.Register("cliauth_session", sessionScript)
	_ = redisscript.Register("cliauth_refresh", refreshScript)
	_ = redisscript.Register("cliauth_upgrade", upgradeScript)
	_ = redisscript.Register("cliauth_forget", forgetScript)
	_ = redisscript.Register("cliauth_revoke_all", revokeAllScript)
	_ = redisscript.Register("cliauth_active", activeScript)
	_ = redisscript.Register("cliauth_roster", rosterScript)
	_ = redisscript.Register("cliauth_roster_read", rosterReadScript)
)
