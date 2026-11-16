package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// payload ssh (no arguments): every ssh login as one folder/login line, with an fzf-style search on
// top. Type to filter (fuzzy, space-separated terms all have to match), ↑/↓ to move, enter to
// connect, ctrl+t to open the full table (payload ssh table), esc to quit.

type sshPickItem struct {
	id, path string
	num      int
	jump     bool
	icon     string // the client's emoji or logo, drawn one line tall and two cells wide
}

type sshPickMatch struct {
	item  sshPickItem
	score int
	pos   []int // matched rune positions in item.path, for highlighting
}

// sshPickIcon is the icon a login shows in the list: that of the nearest folder above it with an
// emoji or logo (the client chosen when it was created). A logo is drawn 2 cells × 1 line, the same
// footprint as an emoji, so every row lines up. Logins outside any folder get the login icon.
func sshPickIcon(s *sshStore, n *sshNode, logos map[string]string) string {
	if n.Parent == "" {
		return sshLoginIcon
	}
	for f := s.get(n.Parent); f != nil; f = s.get(f.Parent) {
		if f.Logo != "" {
			if v, ok := logos[f.Logo]; ok {
				return v
			}
			if lines := logoLines(f.Logo, 2, 1); lines != nil {
				logos[f.Logo] = lines[0]
				return lines[0]
			}
		}
		if f.Icon != "" {
			return f.Icon
		}
	}
	return "📁"
}

// sshPickItems lists the logins as folder/login paths, sorted.
func sshPickItems(s *sshStore) []sshPickItem {
	var items []sshPickItem
	logos := map[string]string{}
	for _, n := range s.Nodes {
		if n.Type != sshLogin {
			continue
		}
		args, _ := n.args()
		_, jump := sshTarget(args)
		items = append(items, sshPickItem{id: n.ID, num: n.Num, path: s.path(n.ID), jump: jump != "", icon: sshPickIcon(s, &n, logos)})
	}
	sort.Slice(items, func(i, j int) bool { return strings.ToLower(items[i].path) < strings.ToLower(items[j].path) })
	return items
}

// fuzzyMatch scores how well query matches text, fzf-style: every space-separated term has to appear
// in text as a subsequence (case-insensitive). Consecutive runs, matches at the start of a path
// segment and a contiguous substring score higher; gaps cost a little. ok is false when a term is
// missing; pos are the matched rune positions.
func fuzzyMatch(query, text string) (score int, pos []int, ok bool) {
	t := []rune(strings.ToLower(text))
	used := map[int]bool{}
	for _, term := range strings.Fields(strings.ToLower(query)) {
		q := []rune(term)
		best, bestPos := -1<<31, []int(nil)
		for start := range t {
			if t[start] != q[0] {
				continue
			}
			sc, ps := 0, []int{start}
			sc += fuzzyBonus(t, start)
			prev, qi := start, 1
			for i := start + 1; i < len(t) && qi < len(q); i++ {
				if t[i] != q[qi] {
					continue
				}
				if i == prev+1 {
					sc += 8 // consecutive
				} else {
					sc -= min(i-prev-1, 6) // gap
					sc += fuzzyBonus(t, i)
				}
				ps = append(ps, i)
				prev, qi = i, qi+1
			}
			if qi < len(q) {
				break // later starts can't fit more of the term either
			}
			sc += 16 // matched the whole term
			if len(ps) == len(q) && ps[len(ps)-1]-ps[0] == len(q)-1 {
				sc += 10 // contiguous substring
			}
			if start == 0 {
				sc += 6
			}
			if sc > best {
				best, bestPos = sc, ps
			}
		}
		if bestPos == nil {
			return 0, nil, false
		}
		score += best
		for _, p := range bestPos {
			used[p] = true
		}
	}
	for p := range used {
		pos = append(pos, p)
	}
	sort.Ints(pos)
	return score - len(t)/8, pos, true // shorter paths win ties
}

// fuzzyBonus rewards a match at the start of a word: after / - _ . space, or a lower→upper change.
func fuzzyBonus(t []rune, i int) int {
	if i == 0 {
		return 10
	}
	switch t[i-1] {
	case '/':
		return 10
	case '-', '_', '.', ' ':
		return 6
	}
	if unicode.IsDigit(t[i]) && !unicode.IsDigit(t[i-1]) {
		return 3
	}
	return 0
}

func filterSSHPick(items []sshPickItem, query string) []sshPickMatch {
	var out []sshPickMatch
	if strings.TrimSpace(query) == "" {
		for _, it := range items {
			out = append(out, sshPickMatch{item: it})
		}
		return out
	}
	// Typing a login's number (45 or #45) puts that login first.
	num, numErr := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(query), "#"))
	for _, it := range items {
		if numErr == nil && it.num == num {
			out = append(out, sshPickMatch{item: it, score: 1 << 30})
		} else if sc, pos, ok := fuzzyMatch(query, it.path); ok {
			out = append(out, sshPickMatch{item: it, score: sc, pos: pos})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].score > out[j].score })
	return out
}

type sshPickModel struct {
	items   []sshPickItem
	matches []sshPickMatch
	input   textinput.Model
	cursor  int
	offset  int
	height  int // rows of results that fit
	// numWidth is the widest login number, so the numbers line up right-aligned.
	numWidth int

	action, target string
}

func newSSHPickModel(items []sshPickItem) sshPickModel {
	in := textinput.New()
	in.Prompt = "> "
	in.PromptStyle = lipgloss.NewStyle().Foreground(sshCyan).Bold(true)
	in.Placeholder = "search, e.g. folder2/, folder1 mst01 or a number"
	in.Width = 60 // without a width the placeholder shows only its first letter
	in.Focus()
	m := sshPickModel{items: items, input: in, height: 15, numWidth: 1}
	for _, it := range items {
		m.numWidth = max(m.numWidth, len(strconv.Itoa(it.num)))
	}
	m.matches = filterSSHPick(items, "")
	return m
}

func (m sshPickModel) Init() tea.Cmd { return textinput.Blink }

func (m sshPickModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.height = max(3, msg.Height-6)
		m.clamp()
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			return m, tea.Quit
		case "enter":
			if len(m.matches) > 0 {
				m.action, m.target = "connect", m.matches[m.cursor].item.id
			}
			return m, tea.Quit
		case "ctrl+t":
			m.action = "table"
			return m, tea.Quit
		case "up", "ctrl+p", "ctrl+k", "shift+tab":
			if m.cursor > 0 {
				m.cursor--
			} else if len(m.matches) > 0 {
				m.cursor = len(m.matches) - 1 // wrap, like cycling through history
			}
			m.clamp()
			return m, nil
		case "down", "ctrl+n", "ctrl+j", "tab":
			if m.cursor < len(m.matches)-1 {
				m.cursor++
			} else {
				m.cursor = 0
			}
			m.clamp()
			return m, nil
		case "pgup":
			m.cursor = max(0, m.cursor-m.height)
			m.clamp()
			return m, nil
		case "pgdown":
			m.cursor = max(0, min(len(m.matches)-1, m.cursor+m.height))
			m.clamp()
			return m, nil
		}
	}
	before := m.input.Value()
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	if m.input.Value() != before {
		m.matches = filterSSHPick(m.items, m.input.Value())
		m.cursor, m.offset = 0, 0
	}
	return m, cmd
}

// clamp keeps the cursor on a match and inside the visible window.
func (m *sshPickModel) clamp() {
	m.cursor = max(0, min(m.cursor, len(m.matches)-1))
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+m.height {
		m.offset = m.cursor - m.height + 1
	}
}

func (m sshPickModel) View() string {
	var b strings.Builder
	b.WriteString(styleSSHBadge.Render("ssh") + "  " + m.input.View() + "\n")
	b.WriteString(styleMuted.Render(fmt.Sprintf("  %d/%d", len(m.matches), len(m.items))) + "\n")
	if len(m.items) == 0 {
		b.WriteString(styleMuted.Render("  no ssh logins yet — payload ssh new adds one") + "\n")
	} else if len(m.matches) == 0 {
		b.WriteString(styleMuted.Render("  no matches") + "\n")
	}
	hl := lipgloss.NewStyle().Foreground(sshCyan).Bold(true)
	sel := lipgloss.NewStyle().Bold(true)
	end := min(len(m.matches), m.offset+m.height)
	for i := m.offset; i < end; i++ {
		mt := m.matches[i]
		at := map[int]bool{}
		for _, p := range mt.pos {
			at[p] = true
		}
		var line strings.Builder
		for j, r := range []rune(mt.item.path) {
			switch {
			case at[j]:
				line.WriteString(hl.Render(string(r)))
			case i == m.cursor:
				line.WriteString(sel.Render(string(r)))
			default:
				line.WriteString(string(r))
			}
		}
		if mt.item.jump {
			line.WriteString(styleMuted.Render(" - has jump"))
		}
		marker := "  "
		if i == m.cursor {
			marker = hl.Render("▌ ")
		}
		num := styleMuted.Render(fmt.Sprintf("%*d", m.numWidth, mt.item.num))
		if i == m.cursor {
			num = hl.Render(fmt.Sprintf("%*d", m.numWidth, mt.item.num))
		}
		b.WriteString(marker + num + "  " + mt.item.icon + " " + line.String() + "\n")
	}
	b.WriteString("\n" + styleSmall.Render("↑/↓ move · enter connect · ctrl+t table · esc quit"))
	return b.String()
}

// sshPickCmd is payload ssh with no arguments: pick a login and connect.
func sshPickCmd() {
	s := mustLoadSSH()
	items := sshPickItems(s)
	if !interactive {
		for _, it := range items {
			line := fmt.Sprintf("%d\t%s", it.num, it.path)
			if it.jump {
				line += " - has jump"
			}
			fmt.Println(line)
		}
		return
	}
	res, err := tea.NewProgram(newSSHPickModel(items)).Run()
	if err != nil {
		fail("%v", err)
	}
	m := res.(sshPickModel)
	switch m.action {
	case "connect":
		if n := s.get(m.target); n != nil {
			sshConnect(s, n, nil)
		}
	case "table":
		sshTableCmd()
	}
}

// sshNewCmd is payload ssh new: the new ssh login form (client folder, name, command, password, jump
// host). With no client folders yet it asks for one first.
func sshNewCmd() {
	if !interactive {
		fail("payload ssh new needs a terminal")
	}
	s := mustLoadSSH()
	if len(s.folders()) == 0 {
		fmt.Println(styleMuted.Render("No client folders yet — add one first (e.g. folder1)."))
		msg := sshFolderForm(s, nil, "")
		fmt.Println(msg)
		if s = mustLoadSSH(); len(s.folders()) == 0 {
			return
		}
	}
	msg := sshLoginForm(s, nil, "")
	fmt.Println(msg)
	if strings.Contains(msg, "✓") {
		s = mustLoadSSH()
		var newest *sshNode
		for i := range s.Nodes {
			if n := &s.Nodes[i]; n.Type == sshLogin && (newest == nil || n.Num > newest.Num) {
				newest = n
			}
		}
		if newest != nil {
			fmt.Println(styleMuted.Render(fmt.Sprintf("  connect: payload ssh %d   ·   copy password: payload ssh %d -p", newest.Num, newest.Num)))
		}
	}
}
