package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Checks and repairs for a curl command before it is saved. Text-level fixes keep the user's
// layout; when a fix has to change an argument, the command is written back one option per line.

var (
	// continuationSpace is a line-continuation \ (or cmd's ^) followed by stray spaces, which
	// breaks the continuation in a real shell.
	continuationSpace = regexp.MustCompile(`([\\^])[ \t]+\n`)
	shellPrompt       = regexp.MustCompile(`^(\$|>|PS>|PS [^>\n]*>)\s+`)
	smartQuotes       = strings.NewReplacer("“", `"`, "”", `"`, "„", `"`, "‘", "'", "’", "'")
)

// dataFlags take a request body as their value.
var dataFlags = map[string]bool{"-d": true, "--data": true, "--data-raw": true, "--data-binary": true, "--data-ascii": true, "--json": true}

// fixCurlCommand returns the command to save, what it fixed, and warnings about problems it
// couldn't fix. err means the command can't be used at all.
func fixCurlCommand(s string) (fixed string, fixes, warnings []string, err error) {
	s = strings.TrimPrefix(s, string(rune(0xFEFF)))
	s = strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
	if s == "" {
		return "", nil, nil, fmt.Errorf("empty command")
	}
	if p := shellPrompt.FindString(s); p != "" {
		s = s[len(p):]
		fixes = append(fixes, "removed the leading shell prompt "+strings.TrimSpace(p))
	}
	if continuationSpace.MatchString(s) {
		s = continuationSpace.ReplaceAllString(s, "$1\n")
		fixes = append(fixes, "removed spaces after a line-continuation \\")
	}
	// Curly quotes come from pasting through chat apps and word processors; a shell never treats
	// them as quotes, so straighten them whenever the result still parses.
	if straight := smartQuotes.Replace(s); straight != s {
		if _, err := splitCommand(straight); err == nil {
			s = straight
			fixes = append(fixes, "replaced curly quotes with straight quotes")
		}
	}
	words, err := splitCommand(s)
	if err != nil {
		return "", nil, nil, err
	}

	switch first := strings.ToLower(words[0]); {
	case first == "curl" || first == "curl.exe":
	case strings.HasPrefix(first, "-") || strings.Contains(first, "://") || strings.HasPrefix(first, "{{"):
		s = "curl " + s
		words = append([]string{"curl"}, words...)
		fixes = append(fixes, "added the missing curl at the start")
	default:
		return "", nil, nil, fmt.Errorf("not a curl command (it starts with %q)", words[0])
	}

	rebuild := false
	for i := 1; i < len(words); i++ {
		flag, value, glued := splitFlag(words[i])
		if !glued && i+1 < len(words) && (dataFlags[flag] || flag == "-X" || flag == "--request") {
			value = words[i+1]
		}
		set := func(v string) {
			if glued {
				words[i] = flag + "=" + v
			} else {
				words[i+1] = v
			}
			rebuild = true
		}
		switch {
		case flag == "-X" || flag == "--request":
			if up := strings.ToUpper(value); up != value {
				set(up)
				fixes = append(fixes, fmt.Sprintf("upper-cased the method %s → %s", value, up))
			}
		case dataFlags[flag]:
			v, fix, warning := fixJSONBody(flag, value)
			if fix != "" {
				set(v)
				fixes = append(fixes, fix)
			}
			if warning != "" {
				warnings = append(warnings, warning)
			}
		case flag == "-H" || flag == "--header":
			if i+1 < len(words) && !strings.ContainsAny(words[i+1], ":;") && !strings.HasPrefix(words[i+1], "@") {
				warnings = append(warnings, fmt.Sprintf("header %q has no \":\" (expected Name: value)", words[i+1]))
			}
		}
	}
	if _, url := describeCurl(words); url == "" {
		warnings = append(warnings, "no URL found in the command")
	}
	if rebuild {
		s = formatCurl(words)
	}
	return s, fixes, warnings, nil
}

// splitFlag separates "--data=value" into flag and value; other words come back as the flag.
func splitFlag(w string) (flag, value string, glued bool) {
	if strings.HasPrefix(w, "--") {
		if f, v, ok := strings.Cut(w, "="); ok {
			return f, v, true
		}
	}
	return w, "", false
}

// fixJSONBody checks a body that looks like JSON. It drops trailing commas when that makes it
// valid, and otherwise warns. Placeholders count as values, so {{id}} is fine anywhere.
func fixJSONBody(flag, body string) (fixed, fix, warning string) {
	t := strings.TrimSpace(body)
	if t == "" || (t[0] != '{' && t[0] != '[') {
		return body, "", ""
	}
	check := func(s string) error {
		var v any
		return json.Unmarshal([]byte(placeholder.ReplaceAllString(s, "0")), &v)
	}
	err := check(body)
	if err == nil {
		return body, "", ""
	}
	if trimmed := dropTrailingCommas(body); trimmed != body && check(trimmed) == nil {
		return trimmed, "removed trailing commas from the " + flag + " JSON", ""
	}
	return body, "", fmt.Sprintf("the %s body is not valid JSON (%v) — saved as-is", flag, err)
}

// dropTrailingCommas removes commas that are followed only by whitespace and } or ], outside strings.
func dropTrailingCommas(s string) string {
	var b strings.Builder
	inString, escaped := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inString:
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
		case c == '"':
			inString = true
		case c == ',':
			rest := strings.TrimLeft(s[i+1:], " \t\r\n")
			if rest != "" && (rest[0] == '}' || rest[0] == ']') {
				continue
			}
		}
		b.WriteByte(c)
	}
	return b.String()
}

// describeCurl finds the method and URL a curl command will use.
func describeCurl(words []string) (method, url string) {
	hasBody := false
	for i := 1; i < len(words); i++ {
		flag, value, glued := splitFlag(words[i])
		if !glued && i+1 < len(words) {
			value = words[i+1]
		}
		switch {
		case flag == "-X" || flag == "--request":
			method = value
		case flag == "-I" || flag == "--head":
			if method == "" {
				method = "HEAD"
			}
		case dataFlags[flag] || flag == "-F" || flag == "--form":
			hasBody = true
		case flag == "--url":
			if url == "" {
				url = value
			}
		case url == "" && !strings.HasPrefix(words[i], "-") &&
			(strings.Contains(words[i], "://") || strings.HasPrefix(words[i], "{{")):
			url = words[i]
		}
	}
	if method == "" {
		method = "GET"
		if hasBody {
			method = "POST"
		}
	}
	return method, url
}

// formatCurl writes words back as a command, one option (with its value) per line.
func formatCurl(words []string) string {
	var b strings.Builder
	b.WriteString(shellQuote(words[0]))
	for _, w := range words[1:] {
		if strings.HasPrefix(w, "-") {
			b.WriteString(" \\\n  ")
		} else {
			b.WriteString(" ")
		}
		b.WriteString(shellQuote(w))
	}
	return b.String()
}
