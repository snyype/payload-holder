package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-isatty"
	"github.com/muesli/termenv"
)

// Terminal capabilities, decided once at startup.
var (
	// interactive: stdin and stdout are real consoles, so arrow-key menus work.
	interactive bool
	// stdoutTTY: stdout is a terminal (console or mintty pty), so colors and boxes are used.
	stdoutTTY bool
)

func initTerminal() {
	in, out := os.Stdin.Fd(), os.Stdout.Fd()
	interactive = isatty.IsTerminal(in) && isatty.IsTerminal(out)
	mintty := isatty.IsCygwinTerminal(out)
	stdoutTTY = isatty.IsTerminal(out) || mintty

	// Windows consoles need VT processing switched on for ANSI colors.
	_, _ = termenv.EnableVirtualTerminalProcessing(termenv.NewOutput(os.Stdout))
	if mintty && os.Getenv("NO_COLOR") == "" {
		// mintty speaks ANSI but looks like a pipe to the detector.
		lipgloss.SetColorProfile(termenv.ANSI256)
	}
}

var (
	accent  = lipgloss.AdaptiveColor{Light: "#047857", Dark: "#34D399"}
	muted   = lipgloss.AdaptiveColor{Light: "#6B7280", Dark: "#8B949E"}
	danger  = lipgloss.AdaptiveColor{Light: "#B91C1C", Dark: "#F87171"}
	warning = lipgloss.AdaptiveColor{Light: "#B45309", Dark: "#FBBF24"}

	jsonKey    = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#1D4ED8", Dark: "#7DD3FC"})
	jsonString = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#047857", Dark: "#86EFAC"})
	jsonNumber = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#B45309", Dark: "#FDBA74"})
	jsonLit    = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#7C3AED", Dark: "#C4B5FD"})
	jsonPunct  = lipgloss.NewStyle().Foreground(muted)

	styleBadge   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#052E1F")).Background(lipgloss.Color("#34D399")).Padding(0, 1)
	styleTitle   = lipgloss.NewStyle().Bold(true)
	styleMuted   = lipgloss.NewStyle().Foreground(muted)
	styleAccent  = lipgloss.NewStyle().Foreground(accent).Bold(true)
	styleOK      = lipgloss.NewStyle().Foreground(accent)
	styleErr     = lipgloss.NewStyle().Foreground(danger)
	styleWarn    = lipgloss.NewStyle().Foreground(warning)
	styleCode    = lipgloss.NewStyle().Foreground(accent)
	styleJSONBar = lipgloss.NewStyle().Border(lipgloss.ThickBorder(), false, false, false, true).
			BorderForeground(accent).PaddingLeft(1)
)

func theme() *huh.Theme {
	t := huh.ThemeCharm()
	t.Focused.Title = t.Focused.Title.Foreground(accent)
	t.Focused.SelectSelector = t.Focused.SelectSelector.Foreground(accent)
	t.Focused.MultiSelectSelector = t.Focused.MultiSelectSelector.Foreground(accent)
	t.Focused.SelectedOption = t.Focused.SelectedOption.Foreground(accent)
	t.Focused.SelectedPrefix = t.Focused.SelectedPrefix.Foreground(accent)
	t.Focused.FocusedButton = t.Focused.FocusedButton.Background(accent).Foreground(lipgloss.Color("#052E1F"))
	t.Focused.Base = t.Focused.Base.BorderForeground(accent)
	return t
}

// runForm runs a single-field huh form; Ctrl+C / Esc exits quietly.
func runForm(field huh.Field) {
	err := huh.NewForm(huh.NewGroup(field)).WithTheme(theme()).WithShowHelp(true).Run()
	if err == huh.ErrUserAborted {
		fmt.Println(styleMuted.Render("Cancelled."))
		os.Exit(130)
	}
	if err != nil {
		fail("%v", err)
	}
}

// --- messages -------------------------------------------------------------------------------

func success(format string, a ...any) {
	fmt.Println(styleOK.Render("✓ ") + fmt.Sprintf(format, a...))
}

func warn(format string, a ...any) {
	fmt.Println(styleWarn.Render("! ") + fmt.Sprintf(format, a...))
}

func info(format string, a ...any) {
	fmt.Println(styleMuted.Render(fmt.Sprintf(format, a...)))
}

// fail prints an error to stderr and exits with status 1.
func fail(format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	if stdoutTTY {
		fmt.Fprintln(os.Stderr, styleErr.Render("✗ ")+msg)
	} else {
		fmt.Fprintln(os.Stderr, msg)
	}
	os.Exit(1)
}

// --- JSON display ---------------------------------------------------------------------------

// showPayload prints a key's JSON: a header and highlighted, indented JSON on a terminal,
// or the stored bytes untouched when stdout is piped.
func showPayload(key string, data []byte, header bool) {
	if !stdoutTTY {
		if header {
			fmt.Printf("----- %s -----\n", key)
		}
		os.Stdout.Write(data)
		if len(data) > 0 && data[len(data)-1] != '\n' {
			fmt.Println()
		}
		return
	}

	var pretty bytes.Buffer
	valid := json.Indent(&pretty, bytes.TrimSpace(data), "", "  ") == nil
	body := strings.TrimRight(string(data), "\r\n")
	if valid {
		body = highlightJSON(pretty.String())
	}

	lines := strings.Count(strings.TrimRight(string(data), "\n"), "\n") + 1
	meta := fmt.Sprintf("%s · %d lines", humanSize(len(data)), lines)
	if !valid {
		meta += " · " + styleWarn.Render("not valid JSON")
	}
	fmt.Println(styleBadge.Render(key) + "  " + styleMuted.Render(meta))
	fmt.Println(styleJSONBar.Render(body))
}

// highlightJSON colors already-indented JSON text token by token.
func highlightJSON(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '"':
			j := i + 1
			for j < len(s) && s[j] != '"' {
				if s[j] == '\\' {
					j++
				}
				j++
			}
			j++ // closing quote
			if j > len(s) {
				j = len(s)
			}
			tok := s[i:j]
			k := j
			for k < len(s) && (s[k] == ' ' || s[k] == '\t') {
				k++
			}
			if k < len(s) && s[k] == ':' {
				b.WriteString(jsonKey.Render(tok))
			} else {
				b.WriteString(jsonString.Render(tok))
			}
			i = j
		case c == '-' || (c >= '0' && c <= '9'):
			j := i + 1
			for j < len(s) && strings.IndexByte("0123456789.eE+-", s[j]) >= 0 {
				j++
			}
			b.WriteString(jsonNumber.Render(s[i:j]))
			i = j
		case c == 't' || c == 'f' || c == 'n':
			j := i
			for j < len(s) && s[j] >= 'a' && s[j] <= 'z' {
				j++
			}
			b.WriteString(jsonLit.Render(s[i:j]))
			i = j
		case strings.IndexByte("{}[],:", c) >= 0:
			b.WriteString(jsonPunct.Render(string(c)))
			i++
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}

func humanSize(n int) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
}

// --- interactive pickers --------------------------------------------------------------------

func keyOptions(ks []string) []huh.Option[string] {
	opts := make([]huh.Option[string], len(ks))
	for i, k := range ks {
		opts[i] = huh.NewOption(k, k)
	}
	return opts
}

// pickKeyTUI shows an arrow-key menu (type / to filter) and returns the chosen key.
func pickKeyTUI(title string, ks []string) string {
	var choice string
	runForm(huh.NewSelect[string]().
		Title(title).
		Description(fmt.Sprintf("%d saved · ↑/↓ to move · / to filter · enter to choose", len(ks))).
		Options(keyOptions(ks)...).
		Height(min(len(ks)+2, 14)).
		Value(&choice))
	return choice
}

// pickKeysTUI shows a checkbox list and returns every ticked key.
func pickKeysTUI(title string, ks []string) []string {
	var chosen []string
	runForm(huh.NewMultiSelect[string]().
		Title(title).
		Description("space to tick · ctrl+a to tick all · / to filter · enter to confirm").
		Options(keyOptions(ks)...).
		Height(min(len(ks)+2, 14)).
		Validate(func(v []string) error {
			if len(v) == 0 {
				return fmt.Errorf("tick at least one key")
			}
			return nil
		}).
		Value(&chosen))
	return chosen
}

// askKeyTUI asks for a new key name with inline validation.
func askKeyTUI() string {
	var key string
	runForm(huh.NewInput().
		Title("Key name").
		Description("Saved as <key>.json in " + storeDir()).
		Placeholder("e.g. create_order").
		Validate(func(s string) error {
			if !validKey(s) {
				return fmt.Errorf(`not empty, "." or "..", and none of / \ : * ? " < > |`)
			}
			return nil
		}).
		Value(&key))
	return strings.TrimSpace(key)
}

// confirmTUI asks a yes/no question; the default answer is No.
func confirmTUI(title, description, yes string) bool {
	ok := false
	runForm(huh.NewConfirm().
		Title(title).
		Description(description).
		Affirmative(yes).
		Negative("Cancel").
		Value(&ok))
	return ok
}
