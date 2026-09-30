package ui

import (
	"net/http/httptest"
	"strings"
	"testing"
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
	for _, code := range []string{"admin_unauthorized", "admin_not_configured", "auth_rate_limited",
		"auth_busy", "auth_store_unavailable", "not_found", "unreachable"} {
		if !strings.Contains(body, "  "+code+": ") {
			t.Errorf("the page has no hint for %q", code)
		}
	}
	for _, term := range []string{"new URL(value.public_base_url).origin !== location.origin", "message(wrongEntry(configuredEntry), true)",
		"hint(value.error?.code, value.error?.message)", "throw new Error(hint('unreachable'))", "cli.public_base_url"} {
		if !strings.Contains(body, term) {
			t.Errorf("missing hint contract: %s", term)
		}
	}
}

// The credential is typed once and then forgotten for weeks, so the page
// says where it lives before any refusal: the Secret, its key, and how to
// read it. The values path is written without a chart's own prefix,
// because the same page serves charts that nest the cli block differently.
func TestTheAuthorizationPageSaysWhereTheAdminKeyLives(t *testing.T) {
	w := httptest.NewRecorder()
	Handler().ServeHTTP(w, httptest.NewRequest("GET", "/cli", nil))
	body := w.Body.String()
	for _, term := range []string{`<details id="admin-key-help">`, "cli.adminKeySecret.existingSecret", "<code>admin-key</code>",
		"get secret", "{.data.键名}", "base64 -d"} {
		if !strings.Contains(body, term) {
			t.Errorf("the page does not say where the admin key lives: missing %q", term)
		}
	}
	if strings.Contains(body, "alarmd.cli.") {
		t.Error("the page names one chart's values path")
	}
}
