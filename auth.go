package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// GitHub login for "payload sync", as a GitHub App: its user tokens can only reach the repositories
// the app is installed on, so installing it on the one payload-sync repo limits payload to that repo.
// The app's client ID and secret are built into release binaries (-X main.githubClientID=... from
// the repo's Actions secrets); local builds read PAYLOAD_GITHUB_CLIENT_ID / _SECRET. A secret
// shipped inside a CLI is not really secret (GitHub's own gh CLI does the same); the login is
// protected by the state check, PKCE and the redirect only ever going to 127.0.0.1.
var (
	githubClientID     string
	githubClientSecret string
	githubAppSlug      string // github.com/apps/<slug>, for the "install the app" link

	githubWebURL = "https://github.com"
	githubAPIURL = "https://api.github.com"
)

// authPort is the fixed callback port: a GitHub App's callback URL must match exactly, so the app
// is registered with http://127.0.0.1:47322/callback.
const authPort = 47322

// syncRepoName is the only repo payload syncs to, under the logged-in account.
const syncRepoName = "payload-sync"

// authFile is vault/auth.json. The tokens are encrypted (see secret_*.go); the rest is plain.
type authFile struct {
	Login          string    `json:"login"`
	Name           string    `json:"name,omitempty"`
	Email          string    `json:"email,omitempty"`
	Scheme         string    `json:"scheme"`
	Token          string    `json:"token"`                   // encrypted access token, base64
	Expires        time.Time `json:"expires,omitzero"`        // when the access token expires
	Refresh        string    `json:"refresh_token,omitempty"` // encrypted refresh token, base64
	RefreshExpires time.Time `json:"refresh_expires,omitzero"`
	LoggedIn       time.Time `json:"logged_in"`
}

func authPath() string {
	return filepath.Join(storeDir(), "vault", "auth.json")
}

func oauthApp() (id, secret string) {
	id, secret = githubClientID, githubClientSecret
	if v := os.Getenv("PAYLOAD_GITHUB_CLIENT_ID"); v != "" {
		id = v
	}
	if v := os.Getenv("PAYLOAD_GITHUB_CLIENT_SECRET"); v != "" {
		secret = v
	}
	return id, secret
}

// appInstallURL is where the user gives the app access to the sync repo.
func appInstallURL() string {
	slug := firstNonEmpty(os.Getenv("PAYLOAD_GITHUB_APP_SLUG"), githubAppSlug)
	if slug == "" {
		return githubWebURL + "/settings/installations"
	}
	return githubWebURL + "/apps/" + slug + "/installations/new"
}

func (a authFile) syncRepo() string {
	return a.Login + "/" + syncRepoName
}

func authCmd(args []string) {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "login":
		authLogin()
	case "logout":
		authLogout()
	case "status", "":
		authStatus()
	default:
		fail("unknown auth command %q — use: payload auth login | status | logout", sub)
	}
}

func loadAuth() (authFile, error) {
	var a authFile
	data, err := os.ReadFile(authPath())
	if err != nil {
		return a, err
	}
	if err := json.Unmarshal(data, &a); err != nil {
		return a, fmt.Errorf("%s is not valid JSON: %v", authPath(), err)
	}
	return a, nil
}

// mustAuth loads the login, or exits telling the user to log in.
func mustAuth() authFile {
	a, err := loadAuth()
	if errors.Is(err, os.ErrNotExist) {
		fail("not logged in — run: payload auth login")
	}
	if err != nil {
		fail("%v", err)
	}
	return a
}

func saveAuth(a authFile) error {
	if err := os.MkdirAll(filepath.Dir(authPath()), 0o700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(a, "", "  ")
	return os.WriteFile(authPath(), append(data, '\n'), 0o600)
}

func sealString(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	sealed, err := sealSecret([]byte(s))
	if err != nil {
		return "", fmt.Errorf("could not encrypt the token: %v", err)
	}
	return base64.StdEncoding.EncodeToString(sealed), nil
}

func (a authFile) unseal(s string) (string, error) {
	if a.Scheme != secretScheme {
		return "", fmt.Errorf("the saved login was encrypted with %q on another system — run: payload auth login", a.Scheme)
	}
	sealed, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return "", err
	}
	tok, err := openSecret(sealed)
	if err != nil {
		return "", fmt.Errorf("could not decrypt the saved login (%v) — run: payload auth login", err)
	}
	return string(tok), nil
}

// token returns a working access token, renewing it with the refresh token (and saving the new
// pair) when it has expired or is about to.
func (a *authFile) token() (string, error) {
	if a.Expires.IsZero() || time.Until(a.Expires) > time.Minute {
		return a.unseal(a.Token)
	}
	if a.Refresh == "" || (!a.RefreshExpires.IsZero() && time.Now().After(a.RefreshExpires)) {
		return "", errors.New("the GitHub login has expired — run: payload auth login")
	}
	refresh, err := a.unseal(a.Refresh)
	if err != nil {
		return "", err
	}
	id, secret := oauthApp()
	t, err := requestToken(url.Values{
		"client_id": {id}, "client_secret": {secret},
		"grant_type": {"refresh_token"}, "refresh_token": {refresh},
	})
	if err != nil {
		return "", fmt.Errorf("could not renew the GitHub login (%v) — run: payload auth login", err)
	}
	if err := a.setTokens(t); err != nil {
		return "", err
	}
	if err := saveAuth(*a); err != nil {
		return "", err
	}
	return t.AccessToken, nil
}

// tokenReply is GitHub's answer to a code exchange or a refresh.
type tokenReply struct {
	AccessToken      string `json:"access_token"`
	ExpiresIn        int    `json:"expires_in"`
	RefreshToken     string `json:"refresh_token"`
	RefreshExpiresIn int    `json:"refresh_token_expires_in"`
	Error            string `json:"error"`
	Description      string `json:"error_description"`
}

func (a *authFile) setTokens(t tokenReply) error {
	var err error
	if a.Token, err = sealString(t.AccessToken); err != nil {
		return err
	}
	if a.Refresh, err = sealString(t.RefreshToken); err != nil {
		return err
	}
	a.Scheme = secretScheme
	now := time.Now().UTC()
	a.Expires, a.RefreshExpires = time.Time{}, time.Time{}
	if t.ExpiresIn > 0 {
		a.Expires = now.Add(time.Duration(t.ExpiresIn) * time.Second)
	}
	if t.RefreshExpiresIn > 0 {
		a.RefreshExpires = now.Add(time.Duration(t.RefreshExpiresIn) * time.Second)
	}
	return nil
}

func requestToken(form url.Values) (tokenReply, error) {
	var out tokenReply
	req, _ := http.NewRequest("POST", githubWebURL+"/login/oauth/access_token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "payload/"+version)
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return out, errors.New(networkProblem(err))
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return out, fmt.Errorf("unexpected reply from GitHub (%s)", resp.Status)
	}
	if out.AccessToken == "" {
		return out, errors.New(firstNonEmpty(out.Description, out.Error, resp.Status))
	}
	return out, nil
}

func authLogin() {
	id, secret := oauthApp()
	if id == "" || secret == "" {
		fail("this build has no GitHub app configured — use a release build, or set PAYLOAD_GITHUB_CLIENT_ID and PAYLOAD_GITHUB_CLIENT_SECRET")
	}

	ln, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", authPort))
	if err != nil {
		fail("could not start the login callback on 127.0.0.1:%d (%v) — is another payload auth login running?", authPort, err)
	}
	redirect := fmt.Sprintf("http://127.0.0.1:%d/callback", authPort)
	state := randomHex(16)
	verifier := base64.RawURLEncoding.EncodeToString(randomBytes(32))
	challenge := sha256.Sum256([]byte(verifier))

	authURL := githubWebURL + "/login/oauth/authorize?" + url.Values{
		"client_id":             {id},
		"redirect_uri":          {redirect},
		"state":                 {state},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(challenge[:])},
		"code_challenge_method": {"S256"},
	}.Encode()

	type result struct {
		code string
		err  error
	}
	done := make(chan result, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if q.Get("state") != state {
			// Not the reply to this login (a stale tab, or another page probing 127.0.0.1): refuse it
			// and keep waiting for the real one.
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, loginPage("Not this login", "This link doesn't belong to the login payload is waiting for. Start again with payload auth login."))
			return
		}
		var res result
		switch {
		case q.Get("error") != "":
			res.err = fmt.Errorf("GitHub said: %s", firstNonEmpty(q.Get("error_description"), q.Get("error")))
		case q.Get("code") == "":
			res.err = errors.New("GitHub sent no login code")
		default:
			res.code = q.Get("code")
		}
		if res.err != nil {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, loginPage("Login failed", html.EscapeString(res.err.Error())))
		} else {
			fmt.Fprint(w, loginPage("You're logged in", "Return to your terminal — you can close this tab."))
		}
		select {
		case done <- res:
		default:
		}
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	fmt.Println(styleBadge.Render("payload auth") + "  " + styleMuted.Render("waiting for GitHub on "+redirect))
	if err := openBrowser(authURL); err != nil {
		info("Open this link in your browser:")
	} else {
		info("Your browser should open GitHub. If it doesn't, open:")
	}
	fmt.Println("  " + authURL)
	info("payload can only reach the repos you install its GitHub app on — just %s. Ctrl+C cancels.", syncRepoName)

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	var res result
	select {
	case res = <-done:
	case <-interrupt:
		fail("login cancelled")
	case <-time.After(5 * time.Minute):
		fail("login timed out after 5 minutes — run payload auth login again")
	}
	if res.err != nil {
		fail("%v", res.err)
	}

	t, err := requestToken(url.Values{
		"client_id": {id}, "client_secret": {secret}, "code": {res.code},
		"redirect_uri": {redirect}, "code_verifier": {verifier},
	})
	if err != nil {
		fail("could not finish the login: %v", err)
	}
	gh := &githubClient{token: t.AccessToken}
	var user struct {
		Login, Name, Email string
	}
	if err := gh.do("GET", "/user", nil, &user); err != nil {
		fail("logged in, but could not read your profile: %v", err)
	}
	if email := primaryEmail(gh); email != "" {
		user.Email = email
	}
	a := authFile{Login: user.Login, Name: user.Name, Email: user.Email, LoggedIn: time.Now().UTC()}
	if err := a.setTokens(t); err != nil {
		fail("%v", err)
	}
	if err := saveAuth(a); err != nil {
		fail("could not save the login: %v", err)
	}
	who := styleTitle.Render(user.Login)
	if user.Email != "" {
		who += styleMuted.Render(" (" + user.Email + ")")
	}
	success("Logged in as %s", who)
	info("Token saved encrypted (%s) in %s", secretScheme, authPath())
	switch err := gh.do("GET", "/repos/"+a.syncRepo(), nil, nil); {
	case err == nil:
		info("Next: payload sync push   (to %s)", a.syncRepo())
	case isStatus(err, 404):
		setupSyncAccess(gh, a, interrupt)
	}
}

// authInstallWait is how long login waits for the app to be given access to the sync repo.
var authInstallWait = 5 * time.Minute

// setupSyncAccess opens the GitHub page that gives the app access to the sync repo — the app's install
// page, or the existing installation's settings when it is installed without that repo — then waits
// until the repo is reachable.
func setupSyncAccess(gh *githubClient, a authFile, interrupt <-chan os.Signal) {
	target, what := appInstallURL(), "install the payload app"
	var inst struct {
		Installations []struct {
			ID int64 `json:"id"`
		} `json:"installations"`
	}
	if gh.do("GET", "/user/installations", nil, &inst) == nil && len(inst.Installations) > 0 {
		target = fmt.Sprintf("%s/settings/installations/%d", githubWebURL, inst.Installations[0].ID)
		what = "give the payload app access to " + syncRepoName
	}
	fmt.Println(styleWarn.Render("! ") + "payload can't reach " + a.syncRepo() + " yet.")
	fmt.Println("  If the repo doesn't exist, create it first (private): " +
		githubWebURL + "/new?name=" + syncRepoName + "&visibility=private")
	fmt.Println("  Opening GitHub to " + what + " — choose " + styleTitle.Render("Only select repositories → "+syncRepoName) + ":")
	fmt.Println("  " + target)
	_ = openBrowser(target)
	info("Waiting for access… (Ctrl+C to finish later — payload auth status checks again)")

	tick := time.NewTicker(3 * time.Second)
	defer tick.Stop()
	deadline := time.After(authInstallWait)
	for {
		select {
		case <-tick.C:
			if gh.do("GET", "/repos/"+a.syncRepo(), nil, nil) == nil {
				success("payload can reach %s — next: payload sync push", styleTitle.Render(a.syncRepo()))
				return
			}
		case <-interrupt:
			info("Stopped waiting. When it's set up: payload auth status, then payload sync push")
			return
		case <-deadline:
			fmt.Println(styleWarn.Render("! ") + "Still can't reach " + a.syncRepo() + ". When it's set up:")
			syncRepoHelp(a)
			return
		}
	}
}

// syncRepoHelp explains the one-time setup: a private payload-sync repo with the app installed on it.
func syncRepoHelp(a authFile) {
	fmt.Println("  1. Create a private repo named " + styleTitle.Render(syncRepoName) + ": " +
		githubWebURL + "/new?name=" + syncRepoName + "&visibility=private")
	fmt.Println("  2. Install the payload GitHub app on " + styleTitle.Render("only that repo") + ": " + appInstallURL())
	fmt.Println(styleMuted.Render("  Then run: payload sync push"))
}

func primaryEmail(gh *githubClient) string {
	var emails []struct {
		Email             string
		Primary, Verified bool
	}
	if gh.do("GET", "/user/emails", nil, &emails) != nil {
		return ""
	}
	for _, e := range emails {
		if e.Primary && e.Verified {
			return e.Email
		}
	}
	return ""
}

func authStatus() {
	a, err := loadAuth()
	if errors.Is(err, os.ErrNotExist) {
		info("Not logged in. Run: payload auth login")
		return
	}
	if err != nil {
		fail("%v", err)
	}
	fmt.Println(styleBadge.Render("payload auth"))
	row := func(label, value string) { fmt.Printf("  %-9s %s\n", styleMuted.Render(label), value) }
	row("user", styleTitle.Render(a.Login)+styleMuted.Render(" "+a.Name))
	if a.Email != "" {
		row("email", a.Email)
	}
	row("access", "only "+a.syncRepo()+styleMuted.Render(" (the repos the payload GitHub app is installed on)"))
	row("token", fmt.Sprintf("encrypted (%s) in %s", a.Scheme, authPath()))
	if !a.RefreshExpires.IsZero() {
		row("expires", "renews itself until "+a.RefreshExpires.Local().Format("2 Jan 2006")+" — then log in again")
	}
	tok, err := a.token()
	if err != nil {
		fmt.Println(styleWarn.Render("! ") + err.Error())
		os.Exit(1)
	}
	gh := &githubClient{token: tok}
	if err := gh.do("GET", "/user", nil, nil); err != nil {
		fmt.Println(styleWarn.Render("! ") + "GitHub rejected the token: " + err.Error() + " — run: payload auth login")
		os.Exit(1)
	}
	fmt.Println(styleOK.Render("✓ ") + "GitHub accepts the token")
	if err := gh.do("GET", "/repos/"+a.syncRepo(), nil, nil); isStatus(err, 404) {
		fmt.Println(styleWarn.Render("! ") + "payload can't reach " + a.syncRepo() + ":")
		syncRepoHelp(a)
	} else if err == nil {
		fmt.Println(styleOK.Render("✓ ") + "can reach " + a.syncRepo())
	}
}

func authLogout() {
	a, err := loadAuth()
	if errors.Is(err, os.ErrNotExist) {
		info("Not logged in.")
		return
	}
	if err := os.Remove(authPath()); err != nil {
		fail("could not remove %s: %v", authPath(), err)
	}
	success("Logged out %s — the token was deleted from this computer", styleTitle.Render(a.Login))
	info("To revoke it on GitHub too: %s/settings/apps/authorizations", githubWebURL)
}

func loginPage(title, body string) string {
	return `<!doctype html><meta charset="utf-8"><title>payload — ` + title + `</title>
<body style="font-family:system-ui,sans-serif;display:grid;place-items:center;height:90vh;background:#0d1117;color:#c9d1d9">
<div style="text-align:center"><h1 style="color:#34d399">` + title + `</h1><p>` + body + `</p></div>`
}

// openBrowser opens u in the default browser (a variable so tests can stand in for the browser).
var openBrowser = func(u string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	case "darwin":
		cmd = exec.Command("open", u)
	default:
		cmd = exec.Command("xdg-open", u)
	}
	return cmd.Start()
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		fail("no randomness available: %v", err)
	}
	return b
}

func randomHex(n int) string { return hex.EncodeToString(randomBytes(n)) }

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}
