// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cli

import "fmt"

func (a *App) help(_ []string) int {
	_, err := fmt.Fprintln(a.Out, `alarmd-cli — versioned OB evidence client (Darwin/Linux)

Start here, without reading source code:
  alarmd-cli --version
  alarmd-cli auth listen --url <entry> --port <p> --state <s>   # the command the authorization page shows
  alarmd-cli auth login                    # paste the trusted OB page code; hidden input
  alarmd-cli profile list                  # find environment_id; no secrets shown
  alarmd-cli discover --env <environment_id>
  alarmd-cli describe <operation> --env <environment_id>
  alarmd-cli invoke <operation> --env <environment_id> --input '{"field":"value"}'

Commands:
  auth listen (--url <entry> | --env <id>) --port <p> --state <s> [--timeout 5m]
                                            Wait on 127.0.0.1:<p> for the authorization page to hand over
                                            a code bound to this process's PKCE verifier; exits after one login.
  auth login [--env <id>] [--rebind]        Import code from hidden input or protected stdin (fallback when
                                            the browser and the CLI are on different machines).
  A login pairs this machine: the session renews itself from a rolling renewal credential kept in the
  0600 profile, until the pairing is unused for 30 days, revoked, or the administrator key is rotated.
  Then every command reports credentials_expired with the login page and the auth login command.
             [--ca-cert </absolute/ca.pem>]  Add a private CA for this environment only.
             [--insecure-tls]             Skip certificate/hostname verification for this environment.
                                           Mutually exclusive with --ca-cert; persists after login.
                                           No code/token command argument is accepted.
                                           --rebind explicitly allows a changed origin.
  auth status --env <id>                   Check server session; does not renew it.
  auth logout --env <id>                   Revoke, then conditionally clear local credentials.
                                           Reports when remote revocation is unconfirmed.
  profile list                            List imported environments and expiry hints.
  profile use <environment_id>             Record human preference only; remote --env required.
  discover --env <id>                      Fetch concise current capabilities and next steps.
  diagnose --env <id>                      Diagnose every strategy of the source's active set: reads all pages of
                                           diagnose.environment and proves the coverage across them (rows = universe,
                                           no id twice, verdicts sum to the universe); exit 3 when it does not hold.
  accept --env <id> [--expect-build <prefix>] [--window 5m]
                                           Deployment acceptance: reads the catalog, fleet, Pods, Redis, each
                                           replica's counters, the full diagnosis and the public surface without a
                                           session, then the control source refresh counts again after --window
                                           (0 skips that increase). Each item is PASS, FAIL, INFO, READ_FAILED,
                                           NOT_BUILT or UNDECIDED (a zero with nothing to count yet); status is ok or
                                           failed, exit 1 when any item is FAIL or READ_FAILED. Items with
                                           column=governance list what strategy owners must fix and never fail
                                           it (output refusals whose rule is the strategy's). The items and every
                                           answer they were decided from are saved in meta.result_file.
  describe <operation> --env <id>           Fetch schema, limits, sources and examples.
  invoke <operation> --env <id> [--input <JSON|@file>]
                                           Fetch describe once, then invoke once with its revision.
  --help / -h                             This guide; no configuration or network required.
  --version                               JSON version; no configuration or network required.

Agent rules:
  Always specify --env on remote commands; login may derive it from the code.
  Discover/describe supplies all operation names and input contracts; only diagnose and accept
  compose built-in reads.
  Read next_call as a suggestion. It is never executed automatically.
  An invoke renews only if admitted by the server and due; there is no background keepalive.
  Local expiry is a hint. The server decides whether a token is still valid.
  stdout is JSON except --help; progress goes to stderr. Exit 0=complete, 3=partial,
  1=call/config/protocol error, 2=command/input error. Business health stays in result.
  Channel responses are redacted and atomically saved once in meta.result_file.
  stdout is at most 20 KiB; read that absolute file path when result_omitted=true.
  Do not paste credentials into command arguments, chat, logs or evidence.

Storage and transport:
  OS user configuration directory/alarmd-cli; ALARMD_CLI_CONFIG_DIR can override it.
  Directory mode 0700, files 0600; profiles contain secrets. Evidence is not deleted by logout.
  HTTP or HTTPS follows the deployment entry in the authorization code; no protocol fallback.
  HTTPS certificate/hostname verification is enabled by default.
  Redirects and URL credentials are refused even with --insecure-tls.
  For a private CA, use auth login --ca-cert /absolute/ca.pem or the system trust store.
  The CA path is saved for this environment; keep the PEM file available for later calls.
  Custom CAs extend system roots; certificate and hostname verification remain enabled.
  Explicit auth login --insecure-tls saves a verification exception only for that environment.
  A successful login without --insecure-tls restores normal verification (optionally --ca-cert).
  profile list and meta.client_transport show the saved/actual verification mode.
  HTTP needs no TLS flags; meta.client_transport.encrypted=false identifies HTTP evidence.
  Network deadline 30 seconds; response limit 8 MiB; input limit 1 MiB.
  No automatic retry, schema cache, arbitrary endpoint or built-in business operations.`)
	if err != nil {
		return 1
	}
	return 0
}
