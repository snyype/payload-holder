package main

import (
	_ "embed"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// The release history, built into the binary for "payload changelog". "payload doctor" fetches the
// newer release's copy to show what an update brings.
//
//go:embed CHANGELOG.md
var changelogText string

type changelogEntry struct {
	Version, Date string
	Body          []string
}

// parseChangelog reads "## vX.Y.Z — date" sections and their lines, newest first as written.
func parseChangelog(text string) []changelogEntry {
	var out []changelogEntry
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if rest, ok := strings.CutPrefix(line, "## "); ok {
			v, date, _ := strings.Cut(rest, " ")
			out = append(out, changelogEntry{Version: v, Date: strings.Trim(date, " —-")})
			continue
		}
		if len(out) > 0 {
			out[len(out)-1].Body = append(out[len(out)-1].Body, line)
		}
	}
	for i := range out {
		out[i].Body = trimBlank(out[i].Body)
	}
	return out
}

func trimBlank(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// fetchChangelog downloads CHANGELOG.md as of release tag.
func fetchChangelog(tag string, timeout time.Duration) ([]changelogEntry, error) {
	url := strings.Replace(repoURL, "https://github.com/", "https://raw.githubusercontent.com/", 1) + "/" + tag + "/CHANGELOG.md"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "payload/"+version)
	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s", resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	return parseChangelog(string(b)), nil
}

// releasesBetween keeps the entries newer than from and no newer than to (dev: just to).
func releasesBetween(entries []changelogEntry, from, to string) []changelogEntry {
	var out []changelogEntry
	for _, e := range entries {
		if e.Version == to || (newer(e.Version, from) && !newer(e.Version, to)) {
			if from != "dev" || e.Version == to {
				out = append(out, e)
			}
		}
	}
	return out
}

var (
	mdCode = regexp.MustCompile("`([^`]+)`")
	mdBold = regexp.MustCompile(`\*\*([^*]+)\*\*`)
)

// printChangelog prints entries as styled text: version headings, bullets, `code` and **bold**.
func printChangelog(entries []changelogEntry, indent string) {
	for i, e := range entries {
		if i > 0 {
			fmt.Println()
		}
		fmt.Println(indent + styleAccent.Render(e.Version) + "  " + styleMuted.Render(e.Date))
		for _, line := range e.Body {
			if strings.TrimSpace(line) == "" {
				continue
			}
			line = strings.Replace(line, "- ", styleOK.Render("•")+" ", 1)
			line = mdCode.ReplaceAllStringFunc(line, func(s string) string { return styleCode.Render(s[1 : len(s)-1]) })
			line = mdBold.ReplaceAllStringFunc(line, func(s string) string { return styleTitle.Render(s[2 : len(s)-2]) })
			fmt.Println(indent + "  " + line)
		}
	}
}

// changelogCmd prints the built-in history, or one version's section.
func changelogCmd(args []string) {
	entries := parseChangelog(changelogText)
	if len(args) > 0 {
		want := "v" + strings.TrimPrefix(args[0], "v")
		for _, e := range entries {
			if e.Version == want {
				printChangelog([]changelogEntry{e}, "")
				return
			}
		}
		fail("no changelog entry for %s — this build knows %s to %s", want, entries[len(entries)-1].Version, entries[0].Version)
	}
	if !stdoutTTY {
		fmt.Print(changelogText)
		return
	}
	fmt.Println(styleBadge.Render("payload changelog") + "  " + styleMuted.Render("you have "+version))
	fmt.Println()
	printChangelog(entries, "")
}

// showWhatsNew prints the changes an update from version to latest brings.
func showWhatsNew(latest string) {
	entries, err := fetchChangelog(latest, 10*time.Second)
	if err != nil {
		info("  (could not load what's new: %s)", networkProblem(err))
		return
	}
	news := releasesBetween(entries, version, latest)
	if len(news) == 0 {
		return
	}
	const most = 6
	fmt.Println()
	fmt.Println(styleTitle.Render("What's new") + styleMuted.Render(fmt.Sprintf(" in %s", latest)))
	shown := news
	if len(news) > most {
		shown = news[:most]
	}
	printChangelog(shown, "  ")
	if len(news) > most {
		info("  …and %d older release(s) — run \"payload changelog\" after updating", len(news)-most)
	}
}
