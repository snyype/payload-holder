package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeGitHub is just enough of GitHub's OAuth and git data API, kept in memory, to run
// payload auth login, sync push and sync pull end to end.
type fakeGitHub struct {
	mu        sync.Mutex
	t         *testing.T
	repo      string
	exists    bool // the repo exists and the app is installed on it
	installs  bool // visiting the install page installs the app (the user clicks through it)
	installID int  // non-zero once installed (possibly without the repo)
	access    string
	refresh   string
	refreshes int
	private   bool
	blobs     map[string][]byte
	trees     map[string]map[string]string // tree sha -> path -> blob sha
	commits   map[string]string            // commit sha -> tree sha
	head      string
	challenge string
	n         int
}

func newFakeGitHub(t *testing.T) (*fakeGitHub, *httptest.Server) {
	f := &fakeGitHub{t: t, repo: "octo/payload-sync", private: true,
		blobs: map[string][]byte{}, trees: map[string]map[string]string{}, commits: map[string]string{}}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeGitHub) id(prefix string) string {
	f.n++
	return fmt.Sprintf("%s%039d", prefix, f.n)
}

func (f *fakeGitHub) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	reply := func(code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	p := r.URL.Path
	if p == "/apps/payload-test/installations/new" || p == "/settings/installations/42" {
		// The user installs the app on the repo (or adds the repo to the installation).
		if f.installs {
			f.exists, f.installID = true, 42
		}
		reply(200, map[string]any{})
		return
	}
	if p == "/login/oauth/authorize" {
		q := r.URL.Query()
		f.challenge = q.Get("code_challenge")
		http.Redirect(w, r, q.Get("redirect_uri")+"?code=code-xyz&state="+url.QueryEscape(q.Get("state")), http.StatusFound)
		return
	}
	if p == "/login/oauth/access_token" {
		_ = r.ParseForm()
		if r.Form.Get("client_id") != "cid" || r.Form.Get("client_secret") != "sec" {
			reply(200, map[string]string{"error": "incorrect_client_credentials"})
			return
		}
		if r.Form.Get("grant_type") == "refresh_token" {
			if r.Form.Get("refresh_token") != f.refresh {
				reply(200, map[string]string{"error": "bad_refresh_token"})
				return
			}
			f.refreshes++
		} else {
			sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if r.Form.Get("code") != "code-xyz" || base64.RawURLEncoding.EncodeToString(sum[:]) != f.challenge {
				reply(200, map[string]string{"error": "bad_verification_code"})
				return
			}
		}
		f.access, f.refresh = f.id("tok-"), f.id("ref-")
		reply(200, map[string]any{"access_token": f.access, "expires_in": 28800,
			"refresh_token": f.refresh, "refresh_token_expires_in": 15897600})
		return
	}
	if f.access == "" || r.Header.Get("Authorization") != "Bearer "+f.access {
		reply(401, map[string]string{"message": "Bad credentials"})
		return
	}
	var body map[string]any
	data, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(data, &body)
	base := "/repos/" + f.repo
	switch {
	case p == "/user":
		reply(200, map[string]string{"login": "octo", "name": "Octo Cat"})
	case p == "/user/installations":
		list := []map[string]any{}
		if f.installID != 0 {
			list = append(list, map[string]any{"id": f.installID})
		}
		reply(200, map[string]any{"installations": list})
	case p == "/user/emails":
		reply(200, []map[string]any{{"email": "octo@example.com", "primary": true, "verified": true}})
	case p == base && r.Method == "GET":
		if !f.exists {
			reply(404, map[string]string{"message": "Not Found"})
			return
		}
		reply(200, map[string]any{"private": f.private, "default_branch": "main"})
	case p == base+"/contents/README.md" && r.Method == "PUT":
		if f.head != "" {
			f.t.Errorf("initialised a repo that already has commits")
		}
		content, _ := base64.StdEncoding.DecodeString(body["content"].(string))
		b := gitBlobSHA(content)
		f.blobs[b] = content
		tr := f.id("t")
		f.trees[tr] = map[string]string{"README.md": b}
		c := f.id("c")
		f.commits[c] = tr
		f.head = c
		reply(201, map[string]any{})
	case p == base+"/git/ref/heads/main" && f.head == "":
		reply(409, map[string]string{"message": "Git Repository is empty."})
	case p == base+"/git/ref/heads/main":
		reply(200, map[string]any{"object": map[string]string{"sha": f.head}})
	case strings.HasPrefix(p, base+"/git/commits/"):
		reply(200, map[string]any{"tree": map[string]string{"sha": f.commits[strings.TrimPrefix(p, base+"/git/commits/")]}})
	case strings.HasPrefix(p, base+"/git/trees/"):
		var entries []map[string]string
		for path, sha := range f.trees[strings.TrimPrefix(p, base+"/git/trees/")] {
			entries = append(entries, map[string]string{"path": path, "type": "blob", "sha": sha})
		}
		reply(200, map[string]any{"tree": entries})
	case p == base+"/git/blobs" && r.Method == "POST":
		content, _ := base64.StdEncoding.DecodeString(body["content"].(string))
		sha := gitBlobSHA(content)
		f.blobs[sha] = content
		reply(201, map[string]string{"sha": sha})
	case strings.HasPrefix(p, base+"/git/blobs/"):
		content := f.blobs[strings.TrimPrefix(p, base+"/git/blobs/")]
		reply(200, map[string]string{"content": base64.StdEncoding.EncodeToString(content), "encoding": "base64"})
	case p == base+"/git/trees" && r.Method == "POST":
		tr := f.id("t")
		f.trees[tr] = map[string]string{}
		for _, e := range body["tree"].([]any) {
			m := e.(map[string]any)
			if _, ok := f.blobs[m["sha"].(string)]; !ok {
				f.t.Errorf("tree refers to unknown blob %v", m["sha"])
			}
			f.trees[tr][m["path"].(string)] = m["sha"].(string)
		}
		reply(201, map[string]string{"sha": tr})
	case p == base+"/git/commits" && r.Method == "POST":
		c := f.id("c")
		f.commits[c] = body["tree"].(string)
		if ps := body["parents"].([]any); len(ps) != 1 || ps[0] != f.head {
			f.t.Errorf("commit parents %v, head %s", ps, f.head)
		}
		reply(201, map[string]string{"sha": c})
	case p == base+"/git/refs/heads/main" && r.Method == "PATCH":
		f.head = body["sha"].(string)
		reply(200, map[string]any{})
	default:
		f.t.Errorf("unexpected %s %s", r.Method, r.URL)
		reply(404, map[string]string{"message": "Not Found"})
	}
}

func (f *fakeGitHub) files() map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]string{}
	for path, sha := range f.trees[f.commits[f.head]] {
		out[path] = string(f.blobs[sha])
	}
	return out
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestMain doubles as the payload binary for the end-to-end test: with PAYLOAD_TEST_MAIN set it
// runs main() against the fake GitHub, with a "browser" that follows GitHub's redirect.
func TestMain(m *testing.M) {
	if os.Getenv("PAYLOAD_TEST_MAIN") == "1" {
		githubWebURL, githubAPIURL = os.Getenv("FAKE_GH"), os.Getenv("FAKE_GH")
		authInstallWait = 4 * time.Second
		openBrowser = func(u string) error {
			au, _ := url.Parse(u)
			cb := au.Query().Get("redirect_uri")
			if cb == "" { // not the login page: the user visits it (e.g. installs the app)
				go func() {
					if resp, err := http.Get(u); err == nil {
						resp.Body.Close()
					}
				}()
				return nil
			}
			go func() {
				// A reply with the wrong state is refused and must not end the login...
				if resp, err := http.Get(cb + "?code=evil&state=wrong"); err == nil {
					if resp.StatusCode != http.StatusBadRequest {
						fmt.Println("FORGED REPLY ACCEPTED")
					}
					resp.Body.Close()
				}
				// ...then the real login: GitHub redirects back to the callback.
				if resp, err := http.Get(u); err == nil {
					resp.Body.Close()
				}
			}()
			return nil
		}
		os.Args = append([]string{"payload"}, strings.Fields(os.Getenv("PAYLOAD_TEST_ARGS"))...)
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func runPayload(t *testing.T, gh, store string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "PAYLOAD_TEST_MAIN=1", "FAKE_GH="+gh, "PAYLOAD_STORE="+store,
		"PAYLOAD_TEST_ARGS="+strings.Join(args, " "), "PAYLOAD_NO_UPDATE_CHECK=1", "NO_COLOR=1",
		"PAYLOAD_GITHUB_CLIENT_ID=cid", "PAYLOAD_GITHUB_CLIENT_SECRET=sec", "PAYLOAD_GITHUB_APP_SLUG=payload-test")
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return string(out), code
}

func TestSecretRoundTrip(t *testing.T) {
	if secretScheme == "aes-gcm" {
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	}
	sealed, err := sealSecret([]byte("gho_secret"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(sealed), "gho_secret") {
		t.Fatal("token visible in sealed data")
	}
	got, err := openSecret(sealed)
	if err != nil || string(got) != "gho_secret" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestLoginPushPull(t *testing.T) {
	if secretScheme == "aes-gcm" {
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	}
	f, srv := newFakeGitHub(t)
	a, b := t.TempDir(), t.TempDir()

	out, code := runPayload(t, srv.URL, a, "auth", "login")
	if code != 0 || !strings.Contains(out, "Logged in as octo") || !strings.Contains(out, "octo@example.com") || strings.Contains(out, "FORGED") {
		t.Fatalf("login (%d):\n%s", code, out)
	}
	authFilePath := filepath.Join(a, "vault", "auth.json")
	raw, _ := os.ReadFile(authFilePath)
	if strings.Contains(string(raw), "tok-") || strings.Contains(string(raw), "ref-") {
		t.Fatal("token stored in plain text")
	}
	// App not installed and the user doesn't finish installing: login opens the install page, waits,
	// then says how to finish; push refuses.
	if !strings.Contains(out, "can't reach octo/payload-sync") || !strings.Contains(out, "/apps/payload-test/installations/new") ||
		!strings.Contains(out, "Still can't reach") {
		t.Errorf("login without the repo:\n%s", out)
	}
	if out, code = runPayload(t, srv.URL, a, "sync", "push"); code == 0 || !strings.Contains(out, "visibility=private") {
		t.Errorf("push without the repo (%d):\n%s", code, out)
	}
	// Installed without the repo: login opens that installation's settings instead.
	f.mu.Lock()
	f.installID = 42
	f.mu.Unlock()
	if out, _ = runPayload(t, srv.URL, a, "auth", "login"); !strings.Contains(out, "/settings/installations/42") {
		t.Errorf("login with the app installed elsewhere:\n%s", out)
	}
	// This time the user completes it in the browser: login notices and says so.
	f.mu.Lock()
	f.installs = true
	f.mu.Unlock()
	out, code = runPayload(t, srv.URL, a, "auth", "login")
	if code != 0 || !strings.Contains(out, "payload can reach octo/payload-sync") {
		t.Fatalf("login with install (%d):\n%s", code, out)
	}
	raw, _ = os.ReadFile(authFilePath)

	// An expired access token is renewed with the refresh token, and the new pair is saved.
	var saved map[string]any
	_ = json.Unmarshal(raw, &saved)
	saved["expires"] = "2020-01-01T00:00:00Z"
	edited, _ := json.Marshal(saved)
	writeFile(t, authFilePath, string(edited))
	out, code = runPayload(t, srv.URL, a, "auth", "status")
	if code != 0 || !strings.Contains(out, "GitHub accepts the token") || !strings.Contains(out, "can reach octo/payload-sync") || f.refreshes != 1 {
		t.Fatalf("status with an expired token (%d, %d refreshes):\n%s", code, f.refreshes, out)
	}
	raw, _ = os.ReadFile(authFilePath)
	if strings.Contains(string(raw), "2020-01-01") {
		t.Error("renewed token was not saved")
	}

	// Computer A: two payloads, curl requests, settings with its own name, and an env vault.
	writeFile(t, filepath.Join(a, "alpha.json"), `{"a":1}`)
	writeFile(t, filepath.Join(a, "beta.json"), `{"b":2}`)
	writeFile(t, filepath.Join(a, "vault", "curl.json"), `{"ping":"curl https://example.com"}`)
	writeFile(t, filepath.Join(a, "vault", "env.json"), `{"token":"SECRET"}`)
	writeFile(t, filepath.Join(a, "customization", "settings.json"), `{"name":"laptop","update_check":true}`)
	out, code = runPayload(t, srv.URL, a, "sync", "push")
	if code != 0 || !strings.Contains(out, "Pushed to octo/payload-sync") {
		t.Fatalf("push (%d):\n%s", code, out)
	}
	raw, _ = os.ReadFile(authFilePath)
	files := f.files()
	for _, p := range []string{"README.md", "payloads/alpha.json", "payloads/beta.json", "curl.json", "settings.json"} {
		if _, ok := files[p]; !ok {
			t.Errorf("repo lacks %s", p)
		}
	}
	for p, c := range files {
		if strings.Contains(c, "SECRET") || strings.Contains(c, "laptop") || strings.Contains(p, "env") || strings.Contains(p, "auth") {
			t.Errorf("repo has %s: %q", p, c)
		}
	}
	if out, _ = runPayload(t, srv.URL, a, "sync", "push"); !strings.Contains(out, "already up to date") {
		t.Errorf("second push:\n%s", out)
	}

	// Computer B: its own payload and name. The login file is shared here (same user, same machine).
	writeFile(t, filepath.Join(b, "vault", "auth.json"), string(raw))
	writeFile(t, filepath.Join(b, "gamma.json"), `{"g":3}`)
	writeFile(t, filepath.Join(b, "vault", "env.json"), `{"token":"B-ONLY"}`)
	writeFile(t, filepath.Join(b, "customization", "settings.json"), `{"name":"desktop"}`)
	if out, code = runPayload(t, srv.URL, b, "sync", "pull"); code == 0 || !strings.Contains(out, "-y") {
		t.Errorf("pull without -y when piped should refuse (%d):\n%s", code, out)
	}
	// A hostile path in the repo must be ignored.
	f.mu.Lock()
	f.blobs["evil"] = []byte("x")
	f.trees[f.commits[f.head]]["payloads/../../evil.json"] = "evil"
	f.mu.Unlock()
	out, code = runPayload(t, srv.URL, b, "sync", "pull", "-y")
	if code != 0 || !strings.Contains(out, "Pulled from octo/payload-sync") {
		t.Fatalf("pull (%d):\n%s", code, out)
	}
	read := func(p string) string { d, _ := os.ReadFile(filepath.Join(b, p)); return string(d) }
	if read("alpha.json") != `{"a":1}` || read("beta.json") != `{"b":2}` || !strings.Contains(read("vault/curl.json"), "ping") {
		t.Errorf("pulled files wrong: %q %q %q", read("alpha.json"), read("beta.json"), read("vault/curl.json"))
	}
	if _, err := os.Stat(filepath.Join(b, "gamma.json")); err == nil {
		t.Error("gamma.json (not in the repo) was kept")
	}
	if s := read("customization/settings.json"); !strings.Contains(s, `"desktop"`) || !strings.Contains(s, "update_check") {
		t.Errorf("settings after pull: %s", s)
	}
	if read("vault/env.json") != `{"token":"B-ONLY"}` {
		t.Error("env vault was touched")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(b), "evil.json")); err == nil {
		t.Error("hostile path was written")
	}
	backups, _ := filepath.Glob(filepath.Join(b, ".cache", "sync-backup", "*", "payloads", "gamma.json"))
	if len(backups) != 1 {
		t.Errorf("no backup of gamma.json: %v\n%s", backups, out)
	}

	// B edits and deletes, pushes; the repo follows.
	f.mu.Lock()
	delete(f.trees[f.commits[f.head]], "payloads/../../evil.json")
	f.mu.Unlock()
	writeFile(t, filepath.Join(b, "alpha.json"), `{"a":100}`)
	os.Remove(filepath.Join(b, "beta.json"))
	out, code = runPayload(t, srv.URL, b, "sync", "push")
	if code != 0 || !strings.Contains(out, "~ alpha") || !strings.Contains(out, "- beta") {
		t.Fatalf("push from B (%d):\n%s", code, out)
	}
	if files = f.files(); files["payloads/alpha.json"] != `{"a":100}` || files["payloads/beta.json"] != "" {
		t.Errorf("repo after B's push: %v", files)
	}

	// A public repo is refused.
	f.mu.Lock()
	f.private = false
	f.mu.Unlock()
	if out, code = runPayload(t, srv.URL, a, "sync", "push"); code == 0 || !strings.Contains(out, "public") {
		t.Errorf("public repo (%d):\n%s", code, out)
	}

	out, code = runPayload(t, srv.URL, a, "auth", "logout")
	if code != 0 || !strings.Contains(out, "Logged out octo") {
		t.Errorf("logout (%d):\n%s", code, out)
	}
	if out, code = runPayload(t, srv.URL, a, "sync", "push"); code == 0 || !strings.Contains(out, "not logged in") {
		t.Errorf("push after logout (%d):\n%s", code, out)
	}
}
