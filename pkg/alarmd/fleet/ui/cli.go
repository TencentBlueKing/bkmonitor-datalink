// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package ui

import (
	"html"
	"regexp"
	"strings"
)

// AdminKeySecret is where this deployment keeps the CLI administrator key:
// the namespace, the Secret and the key inside it. Names only; the key's
// value never reaches this package. An empty or malformed name is a part
// the process could not learn, and the login page leaves it as a
// placeholder for the operator to fill in.
type AdminKeySecret struct {
	Namespace string
	Name      string
	Key       string
}

// The login page carries two holes the handler fills once, at construction:
// the command that reads the administrator key, and the line that says
// which of its parts could not be read.
const (
	adminKeyCommandHole = "{{adminKeyCommand}}"
	adminKeyUnknownHole = "{{adminKeyUnknown}}"
)

// Kubernetes' own naming rules. A name outside them cannot be the real one,
// and it would be pasted into a shell, so it is treated as unread rather
// than quoted: every character these allow is safe unquoted.
var (
	namespacePattern  = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)
	secretNamePattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`)
	secretKeyPattern  = regexp.MustCompile(`^[-._a-zA-Z0-9]+$`)
)

func (s AdminKeySecret) namespace() string {
	if namespacePattern.MatchString(s.Namespace) {
		return s.Namespace
	}
	return ""
}

func (s AdminKeySecret) name() string {
	if len(s.Name) <= 253 && secretNamePattern.MatchString(s.Name) {
		return s.Name
	}
	return ""
}

func (s AdminKeySecret) key() string {
	if len(s.Key) <= 253 && s.Key != "." && s.Key != ".." && secretKeyPattern.MatchString(s.Key) {
		return s.Key
	}
	return ""
}

// command is the kubectl line that prints the administrator key, with each
// part the process could not learn left as a placeholder, and the one line
// that names those parts and where each comes from. The line is empty when
// the command can be run as it stands.
//
// The key's default is said, not assumed: a chart that leaves the key
// unset uses admin-key, but a deployment that set another one and did not
// pass it here would be handed a command that reads nothing.
func (s AdminKeySecret) command() (string, string) {
	namespace, name, key := s.namespace(), s.name(), s.key()
	var unknown []string
	if namespace == "" {
		namespace = "命名空间"
		unknown = append(unknown, "命名空间（alarmd 所在的命名空间）")
	}
	if name == "" {
		name = "Secret名"
		unknown = append(unknown, "Secret 名（values 里 cli.adminKeySecret.existingSecret 的值）")
	}
	path := `键名`
	if key == "" {
		unknown = append(unknown, "键名（values 里 cli.adminKeySecret.key 的值，不写时为 admin-key）")
	} else {
		// A dot inside a key would otherwise be read by jsonpath as a step.
		path = strings.ReplaceAll(key, ".", `\.`)
	}
	command := "kubectl -n " + namespace + " get secret " + name + " -o jsonpath='{.data." + path + "}' | base64 -d"
	if len(unknown) == 0 {
		return command, ""
	}
	return command, "未能读到" + strings.Join(unknown, "、") + "，请替换命令里对应的部分。"
}

// renderCLIPage fills the login page's holes. Both are escaped: the names
// come from the environment, and the page is served before any login.
func renderCLIPage(page []byte, secret AdminKeySecret) []byte {
	command, unknown := secret.command()
	if unknown != "" {
		unknown = `<p id="admin-key-unknown" class="muted">` + html.EscapeString(unknown) + `</p>`
	}
	text := strings.Replace(string(page), adminKeyCommandHole, html.EscapeString(command), 1)
	return []byte(strings.Replace(text, adminKeyUnknownHole, unknown, 1))
}
