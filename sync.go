package main

import (
	"bytes"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// payload sync: copies payloads, saved curl requests and settings to a private GitHub repo
// ("push", which makes the repo match this computer) and back ("pull", which makes this computer
// match the repo, after a backup). The env vault, the login and caches are never synced, and the
// per-computer "name" setting stays local. Everything goes through the GitHub REST API, so git
// does not need to be installed.

// Paths inside the sync repo.
const (
	syncPayloadDir = "payloads/"
	syncCurl       = "curl.json"
	syncSSH        = "ssh.json"
	syncLogoDir    = "ssh-logos/"
	syncSettings   = "settings.json"
	syncReadme     = "README.md"
)

func syncCmd(args []string) {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "push":
		syncPush()
	case "pull":
		yes := len(args) > 1 && (args[1] == "-y" || args[1] == "--yes")
		syncPull(yes)
	case "repo":
		fmt.Println(mustAuth().syncRepo())
	case "status", "":
		syncStatus()
	default:
		fail("unknown sync command %q — use: payload sync push | pull [-y] | status | repo", sub)
	}
}

// --- GitHub API ---------------------------------------------------------------------------------

type githubClient struct{ token string }

type githubError struct {
	Status  int
	Message string
}

func (e *githubError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("%s (HTTP %d)", e.Message, e.Status)
	}
	return fmt.Sprintf("HTTP %d", e.Status)
}

func isStatus(err error, code int) bool {
	var ge *githubError
	return errors.As(err, &ge) && ge.Status == code
}

// do calls the GitHub API; body (if any) is sent as JSON and the reply decoded into out (if any).
func (g *githubClient) do(method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, githubAPIURL+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+g.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "payload/"+version)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return errors.New(networkProblem(err))
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 256<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		var e struct{ Message string }
		_ = json.Unmarshal(data, &e)
		return &githubError{Status: resp.StatusCode, Message: e.Message}
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

// syncSession is a logged-in client for the sync repo.
type syncSession struct {
	gh     *githubClient
	auth   authFile
	repo   string
	branch string
}

func openSync() *syncSession {
	a := mustAuth()
	tok, err := a.token()
	if err != nil {
		fail("%v", err)
	}
	s := &syncSession{gh: &githubClient{token: tok}, auth: a, repo: a.syncRepo()}
	var info struct {
		Private       bool   `json:"private"`
		DefaultBranch string `json:"default_branch"`
	}
	err = s.gh.do("GET", "/repos/"+s.repo, nil, &info)
	switch {
	case isStatus(err, 401):
		fail("GitHub rejected the saved login — run: payload auth login")
	case isStatus(err, 404):
		fmt.Fprintln(os.Stderr, styleErr.Render("✗ ")+"payload can't reach "+s.repo+" — it doesn't exist, or the payload app isn't installed on it:")
		syncRepoHelp(a)
		os.Exit(1)
	case err != nil:
		fail("could not reach %s: %v", s.repo, err)
	}
	if !info.Private {
		fail("%s is public — payload only syncs to a private repo (your payloads may hold real data)", s.repo)
	}
	s.branch = firstNonEmpty(info.DefaultBranch, "main")
	return s
}

// initRepo gives a brand-new, empty repo its first commit (the git data API needs one to build on).
func (s *syncSession) initRepo() error {
	return s.gh.do("PUT", "/repos/"+s.repo+"/contents/"+syncReadme, map[string]string{
		"message": "payload sync: start", "content": base64.StdEncoding.EncodeToString([]byte(syncReadmeText)),
	}, nil)
}

// remoteHead returns the branch's commit and its tree, or "" for both when the repo is empty.
func (s *syncSession) remoteHead() (commit, tree string, err error) {
	var ref struct {
		Object struct{ SHA string } `json:"object"`
	}
	err = s.gh.do("GET", "/repos/"+s.repo+"/git/ref/heads/"+s.branch, nil, &ref)
	if isStatus(err, 404) || isStatus(err, 409) { // 409: "Git Repository is empty"
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	var c struct {
		Tree struct{ SHA string } `json:"tree"`
	}
	if err := s.gh.do("GET", "/repos/"+s.repo+"/git/commits/"+ref.Object.SHA, nil, &c); err != nil {
		return "", "", err
	}
	return ref.Object.SHA, c.Tree.SHA, nil
}

// remoteFiles lists the blobs in tree: path -> blob SHA.
func (s *syncSession) remoteFiles(tree string) (map[string]string, error) {
	files := map[string]string{}
	if tree == "" {
		return files, nil
	}
	var t struct {
		Tree []struct {
			Path, Type, SHA string
		} `json:"tree"`
		Truncated bool `json:"truncated"`
	}
	if err := s.gh.do("GET", "/repos/"+s.repo+"/git/trees/"+tree+"?recursive=1", nil, &t); err != nil {
		return nil, err
	}
	if t.Truncated {
		return nil, errors.New("the sync repo is too large to list")
	}
	for _, e := range t.Tree {
		if e.Type == "blob" {
			files[e.Path] = e.SHA
		}
	}
	return files, nil
}

func (s *syncSession) blob(sha string) ([]byte, error) {
	var b struct{ Content, Encoding string }
	if err := s.gh.do("GET", "/repos/"+s.repo+"/git/blobs/"+sha, nil, &b); err != nil {
		return nil, err
	}
	if b.Encoding != "base64" {
		return []byte(b.Content), nil
	}
	return base64.StdEncoding.DecodeString(strings.ReplaceAll(b.Content, "\n", ""))
}

// gitBlobSHA is the SHA git gives content, so unchanged files are not uploaded again.
func gitBlobSHA(data []byte) string {
	h := sha1.New()
	fmt.Fprintf(h, "blob %d\x00", len(data))
	h.Write(data)
	return fmt.Sprintf("%x", h.Sum(nil))
}

// --- local files --------------------------------------------------------------------------------

// localSyncFiles is what push uploads: repo path -> content.
func localSyncFiles() (map[string][]byte, error) {
	dir := storeDir()
	files := map[string][]byte{syncReadme: []byte(syncReadmeText)}
	for _, k := range keys(dir) {
		data, err := os.ReadFile(filepath.Join(dir, k+".json"))
		if err != nil {
			return nil, err
		}
		files[syncPayloadDir+k+".json"] = data
	}
	if data, err := os.ReadFile(curlPath()); err == nil {
		files[syncCurl] = data
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if data, err := os.ReadFile(sshPath()); err == nil {
		files[syncSSH] = data
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if logos, err := os.ReadDir(sshLogoDir()); err == nil {
		for _, l := range logos {
			if validLogoFile(l.Name()) {
				if data, err := os.ReadFile(sshLogoPath(l.Name())); err == nil {
					files[syncLogoDir+l.Name()] = data
				}
			}
		}
	}
	if data, err := os.ReadFile(settingsPath()); err == nil {
		files[syncSettings] = withoutName(data)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return files, nil
}

// withoutName drops the per-computer "name" from settings.json content (left as-is if not JSON).
func withoutName(data []byte) []byte {
	var f map[string]json.RawMessage
	if json.Unmarshal(data, &f) != nil {
		return data
	}
	if _, ok := f["name"]; !ok {
		return data
	}
	delete(f, "name")
	return encodeSettings(f)
}

func encodeSettings(f map[string]json.RawMessage) []byte {
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	_ = enc.Encode(f)
	return out.Bytes()
}

// localPathFor maps a repo path to the file it is pulled into ("" for paths payload doesn't sync,
// including any payload name that isn't a valid key).
func localPathFor(p string) string {
	switch {
	case p == syncCurl:
		return curlPath()
	case p == syncSSH:
		return sshPath()
	case strings.HasPrefix(p, syncLogoDir) && validLogoFile(strings.TrimPrefix(p, syncLogoDir)):
		return sshLogoPath(strings.TrimPrefix(p, syncLogoDir))
	case p == syncSettings:
		return settingsPath()
	case strings.HasPrefix(p, syncPayloadDir) && strings.HasSuffix(p, ".json"):
		k := strings.TrimSuffix(strings.TrimPrefix(p, syncPayloadDir), ".json")
		if validKey(k) && !strings.Contains(k, "/") {
			return filepath.Join(storeDir(), k+".json")
		}
	}
	return ""
}

// describePath is a repo path as shown to the user.
func describePath(p string) string {
	switch {
	case p == syncCurl:
		return "curl requests"
	case p == syncSSH:
		return "ssh logins"
	case strings.HasPrefix(p, syncLogoDir):
		return "client logo " + strings.TrimPrefix(p, syncLogoDir)
	case p == syncSettings:
		return "settings"
	case strings.HasPrefix(p, syncPayloadDir):
		return strings.TrimSuffix(strings.TrimPrefix(p, syncPayloadDir), ".json")
	}
	return p
}

// --- push ---------------------------------------------------------------------------------------

func syncPush() {
	s := openSync()
	local, err := localSyncFiles()
	if err != nil {
		fail("could not read local files: %v", err)
	}
	head, tree, err := s.remoteHead()
	if err == nil && head == "" {
		if err = s.initRepo(); err == nil {
			head, tree, err = s.remoteHead()
		}
	}
	if err != nil {
		fail("could not read %s: %v", s.repo, err)
	}
	remote, err := s.remoteFiles(tree)
	if err != nil {
		fail("could not read %s: %v", s.repo, err)
	}

	var added, changed, removed []string
	entries := []map[string]any{}
	for _, p := range sortedKeys(local) {
		sha := gitBlobSHA(local[p])
		if old, ok := remote[p]; !ok {
			added = append(added, p)
		} else if old != sha {
			changed = append(changed, p)
		}
		if remote[p] != sha {
			var b struct{ SHA string }
			if err := s.gh.do("POST", "/repos/"+s.repo+"/git/blobs", map[string]string{
				"content": base64.StdEncoding.EncodeToString(local[p]), "encoding": "base64",
			}, &b); err != nil {
				fail("could not upload %s: %v", describePath(p), err)
			}
			sha = b.SHA
		}
		entries = append(entries, map[string]any{"path": p, "mode": "100644", "type": "blob", "sha": sha})
	}
	for p := range remote {
		if _, ok := local[p]; !ok {
			removed = append(removed, p)
		}
	}
	sort.Strings(removed)
	if head != "" && len(added)+len(changed)+len(removed) == 0 {
		success("%s is already up to date %s", s.repo, styleMuted.Render(syncCounts(local)))
		return
	}

	var newTree, commit struct{ SHA string }
	if err := s.gh.do("POST", "/repos/"+s.repo+"/git/trees", map[string]any{"tree": entries}, &newTree); err != nil {
		fail("could not build the commit: %v", err)
	}
	parents := []string{}
	if head != "" {
		parents = append(parents, head)
	}
	msg := fmt.Sprintf("payload sync push from %s\n\n%s", clientName(), syncCounts(local))
	if err := s.gh.do("POST", "/repos/"+s.repo+"/git/commits", map[string]any{
		"message": msg, "tree": newTree.SHA, "parents": parents,
	}, &commit); err != nil {
		fail("could not create the commit: %v", err)
	}
	if head == "" {
		err = s.gh.do("POST", "/repos/"+s.repo+"/git/refs", map[string]any{"ref": "refs/heads/" + s.branch, "sha": commit.SHA}, nil)
	} else {
		err = s.gh.do("PATCH", "/repos/"+s.repo+"/git/refs/heads/"+s.branch, map[string]any{"sha": commit.SHA}, nil)
	}
	if isStatus(err, 422) {
		fail("%s changed while pushing — run payload sync push again", s.repo)
	}
	if err != nil {
		fail("could not update %s: %v", s.repo, err)
	}

	printChanges("+", styleOK, added)
	printChanges("~", styleWarn, changed)
	printChanges("-", styleErr, removed)
	success("Pushed to %s %s", styleTitle.Render(s.repo), styleMuted.Render(syncCounts(local)))
	info("%s/%s/commit/%s", githubWebURL, s.repo, commit.SHA[:min(12, len(commit.SHA))])
}

func syncCounts(files map[string][]byte) string {
	n := 0
	for p := range files {
		if strings.HasPrefix(p, syncPayloadDir) {
			n++
		}
	}
	parts := []string{fmt.Sprintf("%d payload(s)", n)}
	if c, ok := files[syncCurl]; ok {
		var m map[string]string
		_ = json.Unmarshal(c, &m)
		parts = append(parts, fmt.Sprintf("%d curl request(s)", len(m)))
	}
	if _, ok := files[syncSettings]; ok {
		parts = append(parts, "settings")
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

func printChanges(mark string, style interface{ Render(...string) string }, paths []string) {
	for _, p := range paths {
		if p == syncReadme {
			continue
		}
		fmt.Println("  " + style.Render(mark) + " " + describePath(p))
	}
}

// --- pull ---------------------------------------------------------------------------------------

func syncPull(yes bool) {
	s := openSync()
	_, tree, err := s.remoteHead()
	if err != nil {
		fail("could not read %s: %v", s.repo, err)
	}
	remote, err := s.remoteFiles(tree)
	if err != nil {
		fail("could not read %s: %v", s.repo, err)
	}
	syncable := 0
	for p := range remote {
		if localPathFor(p) != "" {
			syncable++
		}
	}
	if syncable == 0 { // e.g. only the README: never wipe this computer because of an unused repo
		fail("%s is empty — run payload sync push on the computer that has your payloads", s.repo)
	}
	local, err := localSyncFiles()
	if err != nil {
		fail("could not read local files: %v", err)
	}

	// The plan: what to write and what to delete so this computer matches the repo.
	write := map[string][]byte{}
	var added, changed, removed []string
	for p, sha := range remote {
		if localPathFor(p) == "" {
			continue
		}
		if l, ok := local[p]; ok && gitBlobSHA(l) == sha {
			continue
		}
		data, err := s.blob(sha)
		if err != nil {
			fail("could not download %s: %v", describePath(p), err)
		}
		if p == syncSettings {
			data = keepLocalName(data)
		}
		write[p] = data
		if _, ok := local[p]; ok {
			changed = append(changed, p)
		} else {
			added = append(added, p)
		}
	}
	for p := range local {
		if _, ok := remote[p]; !ok && localPathFor(p) != "" && p != syncSettings {
			removed = append(removed, p) // settings stay when the repo has none
		}
	}
	sort.Strings(added)
	sort.Strings(changed)
	sort.Strings(removed)
	if len(added)+len(changed)+len(removed) == 0 {
		success("Already up to date with %s", styleTitle.Render(s.repo))
		return
	}

	fmt.Println(styleBadge.Render("payload sync pull") + "  " + styleMuted.Render("from "+s.repo))
	printChanges("+", styleOK, added)
	printChanges("~", styleWarn, changed)
	printChanges("-", styleErr, removed)
	if !yes {
		if !interactive {
			fail("pull changes local files — run it again with -y to confirm (payload sync pull -y)")
		}
		if !confirm(fmt.Sprintf("Make this computer match %s?", s.repo),
			"A backup of everything replaced or deleted is kept first", "Pull") {
			info("Nothing changed.")
			return
		}
	}

	backup := filepath.Join(storeDir(), ".cache", "sync-backup", time.Now().Format("20060102-150405"))
	for _, p := range append(append([]string{}, changed...), removed...) {
		src := localPathFor(p)
		data, err := os.ReadFile(src)
		if err != nil {
			continue
		}
		dst := filepath.Join(backup, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err == nil {
			err = os.WriteFile(dst, data, 0o644)
		}
		if err != nil {
			fail("could not back up %s, nothing changed: %v", describePath(p), err)
		}
	}
	for p, data := range write {
		dst := localPathFor(p)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			fail("%v", err)
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			fail("could not write %s: %v", dst, err)
		}
	}
	for _, p := range removed {
		if err := os.Remove(localPathFor(p)); err != nil && !errors.Is(err, os.ErrNotExist) {
			fail("could not delete %s: %v", describePath(p), err)
		}
	}
	success("Pulled from %s", styleTitle.Render(s.repo))
	if len(changed)+len(removed) > 0 {
		info("Backup of replaced and deleted files: %s", backup)
	}
}

// keepLocalName puts this computer's "name" back into pulled settings.
func keepLocalName(data []byte) []byte {
	name := savedClientName()
	var f map[string]json.RawMessage
	if json.Unmarshal(data, &f) != nil {
		return data
	}
	delete(f, "name")
	if name != "" {
		f["name"], _ = json.Marshal(name)
	}
	return encodeSettings(f)
}

// --- status and repo ----------------------------------------------------------------------------

func syncStatus() {
	a, err := loadAuth()
	if err != nil {
		info("Not logged in. Run: payload auth login, then payload sync push")
		return
	}
	fmt.Println(styleBadge.Render("payload sync") + "  " + styleMuted.Render("as "+a.Login))
	fmt.Printf("  %-8s %s\n", styleMuted.Render("repo"), a.syncRepo()+styleMuted.Render("  ("+githubWebURL+"/"+a.syncRepo()+")"))
	local, err := localSyncFiles()
	if err == nil {
		fmt.Printf("  %-8s %s\n", styleMuted.Render("local"), syncCounts(local))
	}
	fmt.Printf("  %-8s %s\n", styleMuted.Render("synced"), "payloads, vault/curl.json, customization/settings.json (not the env vault, login or your name)")
	info("payload sync push  — make the repo match this computer")
	info("payload sync pull  — make this computer match the repo (backs up first)")
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

const syncReadmeText = `# payload sync

This private repo is managed by [payload](https://github.com/snyype/payload-holder).

- ` + "`payloads/<key>.json`" + ` — saved payloads
- ` + "`curl.json`" + ` — saved curl requests
- ` + "`ssh.json`" + ` — ssh client folders and logins (passwords stay in each computer's env vault)
- ` + "`settings.json`" + ` — key bindings and options

` + "`payload sync push`" + ` makes this repo match a computer; ` + "`payload sync pull`" + ` makes a computer match
this repo. The env vault (tokens, URLs) is never uploaded. Edits made here directly are overwritten by the
next push.
`
