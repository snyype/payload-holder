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

// GitHub login for "payload sync". The OAuth App's ID and secret are built into release binaries
// (-X main.githubClientID=... from the repo's Actions secrets); local builds read them from
// PAYLOAD_GITHUB_CLIENT_ID / PAYLOAD_GITHUB_CLIENT_SECRET. A secret shipped inside a CLI is not
// really secret (GitHub's own gh CLI does the same); the login is protected by the state check,
// PKCE and the redirect only ever going to 127.0.0.1.
var (
	githubClientID     string
	githubClientSecret string

	githubWebURL = "https://github.com"
	githubAPIURL = "https://api.github.com"
)

// authScopes: repo to push to the private sync repo, read:user and user:email for the profile.
const authScopes = "repo read:user user:email"

// authFile is vault/auth.json. The token is encrypted (see secret_*.go); the rest is plain.
type authFile struct {
	Login    string    `json:"login"`
	Name     string    `json:"name,omitempty"`
	Email    string    `json:"email,omitempty"`
	Scopes   string    `json:"scopes"`
	Scheme   string    `json:"scheme"`
	Token    string    `json:"token"` // base64 of the encrypted token
	Repo     string    `json:"sync_repo,omitempty"`
	LoggedIn time.Time `json:"logged_in"`
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

// token decrypts the stored token.
func (a authFile) token() (string, error) {
	if a.Scheme != secretScheme {
		return "", fmt.Errorf("the saved login was encrypted with %q on another system — run: payload auth login", a.Scheme)
	}
	sealed, err := base64.StdEncoding.DecodeString(a.Token)
	if err != nil {
		return "", err
	}
	tok, err := openSecret(sealed)
	if err != nil {
		return "", fmt.Errorf("could not decrypt the saved login (%v) — run: payload auth login", err)
	}
	return string(tok), nil
}

func authLogin() {
	id, secret := oauthApp()
	if id == "" || secret == "" {
		fail("this build has no GitHub app configured — use a release build, or set PAYLOAD_GITHUB_CLIENT_ID and PAYLOAD_GITHUB_CLIENT_SECRET")
	}

	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		fail("could not start the login callback on 127.0.0.1: %v", err)
	}
	redirect := fmt.Sprintf("http://127.0.0.1:%d/callback", ln.Addr().(*net.TCPAddr).Port)
	state := randomHex(16)
	verifier := base64.RawURLEncoding.EncodeToString(randomBytes(32))
	challenge := sha256.Sum256([]byte(verifier))

	authURL := githubWebURL + "/login/oauth/authorize?" + url.Values{
		"client_id":             {id},
		"redirect_uri":          {redirect},
		"scope":                 {authScopes},
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
		if q.Get("state") != state {
			// Not the reply to this login (a stale tab, or another page probing 127.0.0.1): refuse it
			// and keep waiting for the real one.
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
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
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
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
	info("Asks for: your profile and email, and repo (to push to your private sync repo). Ctrl+C cancels.")

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

	tok, scopes, err := exchangeCode(id, secret, res.code, redirect, verifier)
	if err != nil {
		fail("could not finish the login: %v", err)
	}
	gh := &githubClient{token: tok}
	var user struct {
		Login, Name, Email string
	}
	if err := gh.do("GET", "/user", nil, &user); err != nil {
		fail("logged in, but could not read your profile: %v", err)
	}
	if email := primaryEmail(gh); email != "" {
		user.Email = email
	}
	sealed, err := sealSecret([]byte(tok))
	if err != nil {
		fail("could not encrypt the token: %v", err)
	}
	a := authFile{
		Login: user.Login, Name: user.Name, Email: user.Email, Scopes: scopes,
		Scheme: secretScheme, Token: base64.StdEncoding.EncodeToString(sealed), LoggedIn: time.Now().UTC(),
	}
	if old, err := loadAuth(); err == nil && old.Login == user.Login {
		a.Repo = old.Repo // keep the chosen sync repo across logins
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
	info("Next: payload sync push   (uploads to %s)", a.syncRepo())
}

// exchangeCode trades the login code for a token.
func exchangeCode(id, secret, code, redirect, verifier string) (token, scopes string, err error) {
	form := url.Values{
		"client_id": {id}, "client_secret": {secret}, "code": {code},
		"redirect_uri": {redirect}, "code_verifier": {verifier},
	}
	req, _ := http.NewRequest("POST", githubWebURL+"/login/oauth/access_token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "payload/"+version)
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return "", "", errors.New(networkProblem(err))
	}
	defer resp.Body.Close()
	var out struct {
		AccessToken string `json:"access_token"`
		Scope       string `json:"scope"`
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return "", "", fmt.Errorf("unexpected reply from GitHub (%s)", resp.Status)
	}
	if out.AccessToken == "" {
		return "", "", errors.New(firstNonEmpty(out.Description, out.Error, resp.Status))
	}
	return out.AccessToken, strings.ReplaceAll(out.Scope, ",", " "), nil
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
	row("scopes", a.Scopes)
	row("sync to", a.syncRepo())
	row("token", fmt.Sprintf("encrypted (%s) in %s", a.Scheme, authPath()))
	tok, err := a.token()
	if err != nil {
		fmt.Println(styleWarn.Render("! ") + err.Error())
		os.Exit(1)
	}
	if err := (&githubClient{token: tok}).do("GET", "/user", nil, nil); err != nil {
		fmt.Println(styleWarn.Render("! ") + "GitHub rejected the token: " + err.Error() + " — run: payload auth login")
		os.Exit(1)
	}
	fmt.Println(styleOK.Render("✓ ") + "GitHub accepts the token")
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
	if id, _ := oauthApp(); id != "" {
		info("To revoke it on GitHub too: %s/settings/connections/applications/%s", githubWebURL, id)
	}
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
