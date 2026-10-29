// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cli

import (
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// The deployment serves the alarmd-cli built from its own commit, and says
// which version that is at api/cli/version. A CLI of another version still
// works -- the channel is versioned on its own -- but its help, its
// commands and the operations it knows can differ from what the deployment
// documents, so the person is told once at login and at each discover, and
// given the command that installs the matching one. There is no self
// update.

// releaseVersion is a version a release build is stamped with, the form the
// Makefile accepts for VERSION (pre-releases included); a local or
// development build is not compared.
var releaseVersion = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?$`)

// versionCheckTimeout bounds the read: the check never holds a command up
// for longer than this.
const versionCheckTimeout = 3 * time.Second

// versionCheck warns on stderr when this CLI is a release of a version other
// than the one the deployment serves. A deployment that does not serve a
// version -- an older one (404), one built without the CLI bundled (503) --
// or that cannot be reached is not a mismatch, and says nothing.
func (a *App) versionCheck(p Profile) {
	if !releaseVersion.MatchString(a.Version) {
		return
	}
	served, ok := a.servedVersion(p)
	if !ok || served == a.Version {
		return
	}
	base := strings.TrimSuffix(p.PublicBaseURL, "/")
	fmt.Fprintf(a.Err, "Warning: this alarmd-cli is %s and the deployment serves %s. Install the matching one: curl -fsSL %s/api/cli/install.sh | sh -s -- --server %s\n",
		a.Version, served, base, base)
}

// servedVersion is the version the deployment says it serves, when it says
// one.
func (a *App) servedVersion(p Profile) (string, bool) {
	client, u, release, err := a.environmentClient(p)
	defer release()
	if err != nil {
		return "", false
	}
	client.Timeout = versionCheckTimeout
	u.Path += "api/cli/version"
	request, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return "", false
	}
	request.Header.Set("User-Agent", "alarmd-cli/"+a.Version)
	response, err := client.Do(request)
	if err != nil {
		return "", false
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", false
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 65))
	if err != nil || len(data) > 64 {
		return "", false
	}
	served := strings.TrimSpace(string(data))
	if !releaseVersion.MatchString(served) {
		return "", false
	}
	return served, true
}
