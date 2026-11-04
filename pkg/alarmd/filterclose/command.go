// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package filterclose

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
)

// The close command's fixed fields (b7e5beddf docs/reference/contracts/
// alert-close.md): a system operator, the strategy_change source, alarmd's
// own operator id.
const (
	operatorKind    = "system"
	operationSource = "strategy_change"
	operatorID      = "alarmd"
)

// Command is one decision's close command: its operation id and the exact
// JSON object every request of it sends.
type Command struct {
	ID    string
	Bytes []byte
}

type commandFields struct {
	TenantID        string `json:"bk_tenant_id"`
	OperationID     string `json:"operation_id"`
	OperatorID      string `json:"operator_id"`
	OperatorKind    string `json:"operator_kind"`
	OperationSource string `json:"operation_source"`
	Reason          string `json:"reason"`
	EffectiveAt     string `json:"effective_at"`
}

// decisionKey is what one decision is: the strategy, the version and
// facts digest it is about, the decision, what it names besides (batch: the
// series or targets of a per-series close, the inactive period of an
// inactive close), and the revision it was taken against. The same
// decision taken again - the next round, another leader - has the same
// key; another batch of the same decision, or the next inactive period,
// has its own, and with it its own command and effective_at.
func decisionKey(scope Scope, version, digest, decision, batch string, revision int64) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{scope.TenantID, scope.EventSourceID, scope.StrategyID, version, digest, decision,
		batch, strconv.FormatInt(revision, 10)}, "\x00")))
	return hex.EncodeToString(sum[:16])
}

// commandLife is how long one decision keeps its command. Within it every
// round, retry and leader sends the same operation id, reason and
// effective_at, so the link answers a repeat as already_closed rather than
// a conflict. Past it the same decision mints a new command: an inactive
// close that recurs every night, or an absent strategy that comes back and
// goes again, must not end tonight's alerts at the time of the first
// night's decision. A close of tens of thousands of alerts runs within it;
// the alerts left when it ends are closed under the next command, each
// alert once, and an item already pending keeps the bytes it was sent with.
const commandLife = 6 * time.Hour

// Commands keeps each decision's command in the runtime Redis, so a retry
// after a restart or on another leader sends the very same bytes.
type Commands struct {
	client redis.UniversalClient
	prefix string
}

func NewCommands(client redis.UniversalClient, prefix string) (*Commands, error) {
	if client == nil || strings.TrimSpace(prefix) == "" || strings.ContainsAny(prefix, "{} \t\r\n") {
		return nil, errors.New("alarmd filterclose: the command store's client and prefix are required")
	}
	return &Commands{client: client, prefix: prefix}, nil
}

func (commands *Commands) key(decision string) string {
	return commands.prefix + ":filterclose:command:v1:" + decision
}

type storedCommand struct {
	ID    string `json:"id"`
	Bytes []byte `json:"bytes"`
}

// Resolve is the decision's command: the one stored for it, or a new one
// minted at now and stored first (SET NX, so two leaders racing keep one).
func (commands *Commands) Resolve(ctx context.Context, key, tenantID, reason string, now time.Time) (Command, error) {
	if read, err := commands.read(ctx, key); err == nil {
		return read, nil
	} else if !errors.Is(err, redis.Nil) {
		return Command{}, err
	}
	minted := mint(key, tenantID, reason, now)
	encoded, err := json.Marshal(storedCommand{ID: minted.ID, Bytes: minted.Bytes})
	if err != nil {
		return Command{}, err
	}
	if err := commands.client.SetNX(ctx, commands.key(key), encoded, commandLife).Err(); err != nil {
		return Command{}, err
	}
	return commands.read(ctx, key)
}

func (commands *Commands) read(ctx context.Context, key string) (Command, error) {
	raw, err := commands.client.Get(ctx, commands.key(key)).Bytes()
	if err != nil {
		return Command{}, err
	}
	var stored storedCommand
	if err := json.Unmarshal(raw, &stored); err != nil || stored.ID == "" || len(stored.Bytes) == 0 {
		return Command{}, errors.New("alarmd filterclose: a stored command is unreadable")
	}
	return Command{ID: stored.ID, Bytes: stored.Bytes}, nil
}

// mint is a new command for the decision: its operation id is the
// decision's key and the second it was minted, its effective_at that
// second.
func mint(key, tenantID, reason string, now time.Time) Command {
	at := now.UTC().Truncate(time.Second)
	sum := sha256.Sum256([]byte(key + "\x00" + strconv.FormatInt(at.Unix(), 10)))
	id := "alarmd-sc-" + hex.EncodeToString(sum[:20])
	encoded, _ := json.Marshal(commandFields{TenantID: tenantID, OperationID: id, OperatorID: operatorID, OperatorKind: operatorKind,
		OperationSource: operationSource, Reason: cutReason(reason), EffectiveAt: at.Format(time.RFC3339)})
	return Command{ID: id, Bytes: encoded}
}

// cutReason is the reason within the link's 256 bytes, a rune cut in two
// dropped.
func cutReason(reason string) string {
	if len(reason) <= 256 {
		return reason
	}
	return strings.ToValidUTF8(reason[:256], "")
}
