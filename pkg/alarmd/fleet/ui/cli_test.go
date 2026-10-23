package ui

import (
	"html"
	"net/http/httptest"
	"strings"
	"testing"

	xhtml "golang.org/x/net/html"
)

func TestCLIAuthorizationPageIsEmbeddedAndNotCached(t *testing.T) {
	for _, path := range []string{"/cli", "/cli/", "/cli.html"} {
		w := httptest.NewRecorder()
		Handler().ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: %d", path, w.Code)
		}
		body := w.Body.String()
		for _, term := range []string{"api/cli/auth/", "confirm:true", "textContent", "pagehide", "alarmd-cli auth login"} {
			if !strings.Contains(body, term) {
				t.Fatalf("missing page contract %s", term)
			}
		}
		if strings.Contains(body, "localStorage") {
			t.Fatal("persistent grant handling in page")
		}
	}
}

func TestCLIAuthorizationPageUsesEphemeralDeploymentKey(t *testing.T) {
	w := httptest.NewRecorder()
	Handler().ServeHTTP(w, httptest.NewRequest("GET", "/cli", nil))
	body := w.Body.String()
	for _, term := range []string{`type="password"`, `autocomplete="off"`, `credentials:'omit'`, `'Authorization':'Bearer '+key`, `currentRevision !== revision`, `adminKey.value = ''`, `issue.disabled = true`} {
		if !strings.Contains(body, term) {
			t.Fatalf("missing direct authorization contract: %s", term)
		}
	}
	for _, term := range []string{"localStorage", "sessionStorage", "credentials:'same-origin'"} {
		if strings.Contains(body, term) {
			t.Fatalf("unwanted persistent or cookie credential use: %s", term)
		}
	}
}

// Every refusal the grant route can give reaches the operator as what to do,
// not only as the server's English sentence: the page names where to look
// for each code. The origin check is also made before the button, from the
// preview's own entry, because a mismatch there is certain to be refused.
func TestTheAuthorizationPageSaysWhatToDoForEveryGrantRefusal(t *testing.T) {
	w := httptest.NewRecorder()
	Handler().ServeHTTP(w, httptest.NewRequest("GET", "/cli", nil))
	body := w.Body.String()
	for _, code := range []string{"admin_unauthorized", "auth_rate_limited",
		"auth_busy", "auth_store_unavailable", "not_found", "unreachable"} {
		if !strings.Contains(body, "  "+code+": ") {
			t.Errorf("the page has no hint for %q", code)
		}
	}
	for _, term := range []string{"new URL(value.public_base_url).origin !== location.origin", "message('inspect-status', wrongEntry(configuredEntry), true)",
		"hint(value.error?.code, value.error?.message)", "throw new Error(hint('unreachable'))", "cli.public_base_url"} {
		if !strings.Contains(body, term) {
			t.Errorf("missing hint contract: %s", term)
		}
	}
}

// The credential is typed once and then forgotten for weeks, so the page
// says where it lives before any refusal: the command that reads it. A page
// that was told nothing still gives the command, every part a placeholder,
// and names where each part comes from. The values path is written without
// a chart's own prefix, because the same page serves charts that nest the
// cli block differently.
func TestTheAuthorizationPageSaysWhereTheAdminKeyLives(t *testing.T) {
	w := httptest.NewRecorder()
	Handler().ServeHTTP(w, httptest.NewRequest("GET", "/cli", nil))
	body := w.Body.String()
	for _, term := range []string{`<details id="admin-key-help">`, "cli.adminKeySecret.existingSecret", "cli.adminKeySecret.key",
		"不写时为 admin-key", "get secret", "{.data.键名}", "base64 -d"} {
		if !strings.Contains(html.UnescapeString(body), term) {
			t.Errorf("the page does not say where the admin key lives: missing %q", term)
		}
	}
	if strings.Contains(body, "alarmd.cli.") {
		t.Error("the page names one chart's values path")
	}
}

// renderedLoginPage serves the login page told where the key is kept, and
// parses it.
func renderedLoginPage(t *testing.T, secret AdminKeySecret) (string, *xhtml.Node) {
	t.Helper()
	w := httptest.NewRecorder()
	Handler(WithAdminKeySecret(secret)).ServeHTTP(w, httptest.NewRequest("GET", "/cli", nil))
	if w.Code != 200 {
		t.Fatalf("/cli = %d", w.Code)
	}
	document, err := xhtml.Parse(strings.NewReader(w.Body.String()))
	if err != nil {
		t.Fatal(err)
	}
	return w.Body.String(), document
}

func elementByID(node *xhtml.Node, id string) *xhtml.Node {
	if node.Type == xhtml.ElementNode {
		for _, attribute := range node.Attr {
			if attribute.Key == "id" && attribute.Val == id {
				return node
			}
		}
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if found := elementByID(child, id); found != nil {
			return found
		}
	}
	return nil
}

func textOf(node *xhtml.Node) string {
	if node == nil {
		return ""
	}
	if node.Type == xhtml.TextNode {
		return node.Data
	}
	var text strings.Builder
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		text.WriteString(textOf(child))
	}
	return text.String()
}

// Told the namespace, the Secret and its key, the page gives the command
// as it would be typed: nothing to replace, and no line about unread parts.
func TestTheLoginPageGivesTheCommandForThisDeployment(t *testing.T) {
	body, page := renderedLoginPage(t, AdminKeySecret{Namespace: "ops-alarmd", Name: "alarmd-cli-admin", Key: "admin-key"})
	const want = `kubectl -n ops-alarmd get secret alarmd-cli-admin -o jsonpath='{.data.admin-key}' | base64 -d`
	if got := textOf(elementByID(page, "admin-key-command")); got != want {
		t.Fatalf("command = %q, want %q", got, want)
	}
	if elementByID(page, "admin-key-unknown") != nil || strings.Contains(body, "未能读到") {
		t.Error("a command with every part known still says a part could not be read")
	}
	if elementByID(page, "copy-admin-key-command") == nil {
		t.Error("the command has no copy button")
	}
	if strings.Contains(body, "{{") {
		t.Error("a hole in the page was left unfilled")
	}
	// A dot in a key is a step to jsonpath unless escaped.
	_, page = renderedLoginPage(t, AdminKeySecret{Namespace: "ops-alarmd", Name: "alarmd-cli-admin", Key: "admin.key"})
	if got := textOf(elementByID(page, "admin-key-command")); !strings.Contains(got, `'{.data.admin\.key}'`) {
		t.Errorf("a dotted key is not escaped for jsonpath: %q", got)
	}
}

// Each part the process could not learn -- or learned in a shape no
// Kubernetes name has, which must not reach a shell -- is a placeholder in
// its own position, and one line names exactly those parts and where each
// comes from. The key is never guessed: its chart default is said, not used.
func TestEachUnreadPartOfTheCommandIsAPlaceholderAndNamed(t *testing.T) {
	known := AdminKeySecret{Namespace: "ops-alarmd", Name: "alarmd-cli-admin", Key: "admin-key"}
	parts := []struct{ placeholder, named string }{
		{"-n 命名空间 ", "命名空间（alarmd 所在的命名空间）"},
		{"secret Secret名 ", "Secret 名（values 里 cli.adminKeySecret.existingSecret 的值）"},
		{"{.data.键名}", "键名（values 里 cli.adminKeySecret.key 的值，不写时为 admin-key）"},
	}
	for _, tc := range []struct {
		name    string
		secret  AdminKeySecret
		unknown []int
	}{
		{"no namespace", AdminKeySecret{Name: known.Name, Key: known.Key}, []int{0}},
		{"no Secret name", AdminKeySecret{Namespace: known.Namespace, Key: known.Key}, []int{1}},
		{"no key", AdminKeySecret{Namespace: known.Namespace, Name: known.Name}, []int{2}},
		{"nothing", AdminKeySecret{}, []int{0, 1, 2}},
		{"namespace that is no name", AdminKeySecret{Namespace: "ops; rm -rf /", Name: known.Name, Key: known.Key}, []int{0}},
		{"Secret name that is no name", AdminKeySecret{Namespace: known.Namespace, Name: "<script>x</script>", Key: known.Key}, []int{1}},
		{"key that is no key", AdminKeySecret{Namespace: known.Namespace, Name: known.Name, Key: "admin key'"}, []int{2}},
	} {
		body, page := renderedLoginPage(t, tc.secret)
		command := textOf(elementByID(page, "admin-key-command"))
		line := textOf(elementByID(page, "admin-key-unknown"))
		if line == "" || strings.Count(line, "（") != len(tc.unknown) {
			t.Errorf("%s: the line names %q, want %d parts", tc.name, line, len(tc.unknown))
		}
		for index, part := range parts {
			unread := false
			for _, u := range tc.unknown {
				unread = unread || u == index
			}
			if strings.Contains(command, part.placeholder) != unread || strings.Contains(line, part.named) != unread {
				t.Errorf("%s: part %d unread=%v, command %q, line %q", tc.name, index, unread, command, line)
			}
		}
		for _, value := range []string{tc.secret.Namespace, tc.secret.Name, tc.secret.Key} {
			if value != "" && !strings.Contains(command, value) && strings.Contains(body, value) {
				t.Errorf("%s: a rejected name %q still reaches the page", tc.name, value)
			}
		}
		if !strings.HasPrefix(command, "kubectl -n ") || !strings.HasSuffix(command, " | base64 -d") {
			t.Errorf("%s: command = %q", tc.name, command)
		}
	}
}

// The page fills both of its holes, so each must be in it exactly once.
func TestTheLoginPageHasEachHoleOnce(t *testing.T) {
	for _, hole := range []string{adminKeyCommandHole, adminKeyUnknownHole} {
		if n := strings.Count(string(pageCLI), hole); n != 1 {
			t.Errorf("%s appears %d times", hole, n)
		}
	}
}

// One thing to do at a time. On load the three steps are in order, each
// holding its own control and the line that answers it; everything that is
// not the main path -- the copy-code fallback, revoking, the session's
// lifetime, where the key lives -- is inside a closed <details>. The only
// buttons in view before anything is opened are the steps' own.
func TestTheLoginPageShowsOneStepAtATimeAndFoldsTheRest(t *testing.T) {
	_, page := renderedLoginPage(t, AdminKeySecret{})
	list := elementByID(page, "step-command").Parent
	var steps []string
	for child := list.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == xhtml.ElementNode && child.Data == "li" {
			steps = append(steps, idOf(child))
		}
	}
	if list.Data != "ol" || strings.Join(steps, " ") != "step-command step-inspect step-authorize" {
		t.Fatalf("the steps, in order: %s %v", list.Data, steps)
	}
	within := func(id, container string) bool {
		for node := elementByID(page, id); node != nil; node = node.Parent {
			if idOf(node) == container {
				return true
			}
		}
		return false
	}
	for container, ids := range map[string][]string{
		"step-command":   {"listen-command", "copy-command", "command-status", "probe", "check-cli"},
		"step-inspect":   {"admin-key", "inspect", "admin-key-help", "preview", "inspect-status"},
		"step-authorize": {"authorize", "authorize-hint", "authorize-status"},
		"manual":         {"fallback", "issue", "grant", "code", "copy", "issue-status"},
		"session-help":   {"revoke", "revoke-status"},
		"admin-key-help": {"admin-key-command", "copy-admin-key-command", "admin-key-status"},
	} {
		for _, id := range ids {
			if !within(id, container) {
				t.Errorf("%s is not inside %s", id, container)
			}
		}
	}
	for _, id := range []string{"manual", "session-help", "admin-key-help"} {
		node := elementByID(page, id)
		if node == nil || node.Data != "details" || hasAttribute(node, "open") {
			t.Errorf("%s is not a closed <details>", id)
		}
	}
	if !strings.Contains(textOf(elementByID(page, "session-help")), "会话有效期 1 小时") {
		t.Error("the session's lifetime is not with the revocation")
	}
	var visible []string
	var walk func(*xhtml.Node, bool)
	walk = func(node *xhtml.Node, folded bool) {
		if node.Type == xhtml.ElementNode && node.Data == "details" && !hasAttribute(node, "open") {
			folded = true
		}
		if node.Type == xhtml.ElementNode && node.Data == "button" && !folded && !hasAttribute(node, "hidden") {
			visible = append(visible, idOf(node))
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child, folded)
		}
	}
	walk(page, false)
	if got := strings.Join(visible, " "); got != "copy-command inspect authorize" {
		t.Errorf("buttons in view on load: %s", got)
	}
	if elementByID(page, "status") != nil {
		t.Error("a single status line far from every control is back")
	}
}

func idOf(node *xhtml.Node) string {
	for _, attribute := range node.Attr {
		if attribute.Key == "id" {
			return attribute.Val
		}
	}
	return ""
}

func hasAttribute(node *xhtml.Node, key string) bool {
	for _, attribute := range node.Attr {
		if attribute.Key == key {
			return true
		}
	}
	return false
}
