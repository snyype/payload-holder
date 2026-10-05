package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
)

const perPage = 10

type tableRow struct {
	key, size, modified, preview string
}

func loadRows(dir string) []tableRow {
	var rows []tableRow
	for _, k := range keys(dir) {
		path := filepath.Join(dir, k+".json")
		st, err := os.Stat(path)
		if err != nil {
			continue
		}
		data, _ := os.ReadFile(path)
		rows = append(rows, tableRow{
			key:      k,
			size:     humanSize(int(st.Size())),
			modified: st.ModTime().Format("2006-01-02 15:04"),
			preview:  previewJSON(data, 40),
		})
	}
	return rows
}

// previewJSON returns the payload compacted onto one line and cut to max runes.
func previewJSON(data []byte, max int) string {
	var buf bytes.Buffer
	s := strings.Join(strings.Fields(string(data)), " ")
	if json.Compact(&buf, bytes.TrimSpace(data)) == nil {
		s = buf.String()
	}
	if r := []rune(s); len(r) > max {
		s = string(r[:max-1]) + "…"
	}
	return s
}

func pageCount(n int) int {
	return max(1, (n+perPage-1)/perPage)
}

// renderPage draws one page of rows as a table; selected is the index within the page (-1 for none).
func renderPage(rows []tableRow, page, selected int) string {
	start := page * perPage
	end := min(start+perPage, len(rows))

	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(muted)).
		Headers("#", "KEY", "SIZE", "MODIFIED", "PREVIEW").
		StyleFunc(func(row, col int) lipgloss.Style {
			s := lipgloss.NewStyle().Padding(0, 1)
			switch {
			case row == table.HeaderRow:
				return s.Bold(true).Foreground(accent)
			case row == selected:
				s = s.Bold(true).Foreground(lipgloss.Color("#052E1F")).Background(lipgloss.Color("#34D399"))
			case col == 0 || col == 2 || col == 3:
				s = s.Foreground(muted)
			case col == 4:
				s = s.Foreground(lipgloss.AdaptiveColor{Light: "#374151", Dark: "#C9D1D9"})
			}
			if col == 0 || col == 2 {
				s = s.Align(lipgloss.Right)
			}
			return s
		})
	for i := start; i < end; i++ {
		r := rows[i]
		t.Row(strconv.Itoa(i+1), r.key, r.size, r.modified, r.preview)
	}
	return t.Render()
}

func tableHeader(rows []tableRow, page int) string {
	return styleBadge.Render("payloads") + "  " + styleMuted.Render(
		fmt.Sprintf("%d saved · page %d of %d", len(rows), page+1, pageCount(len(rows))))
}

// tableCmd shows the keys as a paged table: interactive on a console, otherwise one printed page.
func tableCmd(dir string, args []string) {
	rows := loadRows(dir)
	if len(rows) == 0 {
		info("No saved payloads yet. Run:  payload store")
		return
	}

	page := 0
	if len(args) > 0 {
		n, err := strconv.Atoi(args[0])
		if err != nil || n < 1 || n > pageCount(len(rows)) {
			fail("page must be between 1 and %d", pageCount(len(rows)))
		}
		page = n - 1
	}

	if !interactive {
		fmt.Println(tableHeader(rows, page))
		fmt.Println(renderPage(rows, page, -1))
		if page+1 < pageCount(len(rows)) {
			info("Next page:  payload table %d", page+2)
		}
		return
	}

	m, err := tea.NewProgram(tableModel{rows: rows, page: page}).Run()
	if err != nil {
		fail("%v", err)
	}
	if key := m.(tableModel).chosen; key != "" {
		showPayload(key, readPayload(dir, key), true)
	}
}

type tableModel struct {
	rows   []tableRow
	page   int
	cursor int // index within the current page
	chosen string
}

func (m tableModel) Init() tea.Cmd { return nil }

func (m tableModel) onPage() int {
	return min(perPage, len(m.rows)-m.page*perPage)
}

func (m tableModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	pages := pageCount(len(m.rows))
	switch k.String() {
	case "q", "esc", "ctrl+c":
		return m, tea.Quit
	case "enter":
		m.chosen = m.rows[m.page*perPage+m.cursor].key
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		} else if m.page > 0 {
			m.page--
			m.cursor = m.onPage() - 1
		}
	case "down", "j":
		if m.cursor < m.onPage()-1 {
			m.cursor++
		} else if m.page < pages-1 {
			m.page++
			m.cursor = 0
		}
	case "right", "l", "pgdown", "n", " ":
		if m.page < pages-1 {
			m.page++
			m.cursor = min(m.cursor, m.onPage()-1)
		}
	case "left", "h", "pgup", "p":
		if m.page > 0 {
			m.page--
		}
	case "home", "g":
		m.page, m.cursor = 0, 0
	case "end", "G":
		m.page = pages - 1
		m.cursor = m.onPage() - 1
	}
	return m, nil
}

func (m tableModel) View() string {
	if m.chosen != "" {
		return ""
	}
	pages := pageCount(len(m.rows))
	dots := make([]string, pages)
	for i := range dots {
		if i == m.page {
			dots[i] = styleAccent.Render("●")
		} else {
			dots[i] = styleMuted.Render("○")
		}
	}
	pager := strings.Join(dots, " ")
	if pages > 12 {
		pager = styleAccent.Render(fmt.Sprintf("%d/%d", m.page+1, pages))
	}
	help := styleMuted.Render("←/→ page · ↑/↓ select · enter view · g/G first/last · q quit")
	return tableHeader(m.rows, m.page) + "\n" +
		renderPage(m.rows, m.page, m.cursor) + "\n" +
		"  " + pager + "   " + help + "\n"
}
