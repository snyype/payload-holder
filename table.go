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

// credit is "created by @snyype", with @snyype linking to the author's GitHub profile.
func credit() string {
	return styleMuted.Render("created by ") + hyperlink("https://github.com/snyype/", styleAccent.Render("@snyype"))
}

// hyperlink makes text clickable in terminals that support OSC 8 links; others just show the text.
func hyperlink(url, text string) string {
	if !stdoutTTY {
		return text
	}
	return "\x1b]8;;" + url + "\x1b\\" + text + "\x1b]8;;\x1b\\"
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

	m, err := tea.NewProgram(tableModel{dir: dir, rows: rows, page: page}).Run()
	if err != nil {
		fail("%v", err)
	}
	if key := m.(tableModel).chosen; key != "" {
		showPayload(key, readPayload(dir, key), true)
	}
}

type tableModel struct {
	dir    string
	rows   []tableRow
	page   int
	cursor int // index within the current page
	chosen string
	status string // result of the last copy or delete, shown under the table

	confirmDrop string // key awaiting a y/N answer before it is deleted
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
	m.status = ""
	if k.String() == "ctrl+c" {
		return m, tea.Quit
	}
	if m.confirmDrop != "" {
		return m.answerDrop(k)
	}
	switch {
	case pressed(k, "table", "quit"):
		return m, tea.Quit
	case pressed(k, "table", "delete"):
		m.confirmDrop = m.rows[m.page*perPage+m.cursor].key
	case pressed(k, "table", "copy"):
		key := m.rows[m.page*perPage+m.cursor].key
		data, err := os.ReadFile(filepath.Join(m.dir, key+".json"))
		if err == nil {
			err = copyText(strings.TrimSpace(string(data)))
		}
		if err != nil {
			m.status = styleErr.Render("✗ could not copy " + key + ": " + err.Error())
		} else {
			m.status = styleOK.Render("✓ Copied " + key + " to the clipboard")
		}
	case pressed(k, "table", "view"):
		m.chosen = m.rows[m.page*perPage+m.cursor].key
		return m, tea.Quit
	case pressed(k, "table", "up"):
		if m.cursor > 0 {
			m.cursor--
		} else if m.page > 0 {
			m.page--
			m.cursor = m.onPage() - 1
		}
	case pressed(k, "table", "down"):
		if m.cursor < m.onPage()-1 {
			m.cursor++
		} else if m.page < pages-1 {
			m.page++
			m.cursor = 0
		}
	case pressed(k, "table", "next_page"):
		if m.page < pages-1 {
			m.page++
			m.cursor = min(m.cursor, m.onPage()-1)
		}
	case pressed(k, "table", "prev_page"):
		if m.page > 0 {
			m.page--
		}
	case pressed(k, "table", "first"):
		m.page, m.cursor = 0, 0
	case pressed(k, "table", "last"):
		m.page = pages - 1
		m.cursor = m.onPage() - 1
	}
	return m, nil
}

// answerDrop deletes the key awaiting confirmation on a confirm_delete key; any other key cancels.
func (m tableModel) answerDrop(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := m.confirmDrop
	m.confirmDrop = ""
	if !pressed(k, "table", "confirm_delete") {
		m.status = styleMuted.Render("Not deleted.")
		return m, nil
	}
	if err := os.Remove(filepath.Join(m.dir, key+".json")); err != nil {
		m.status = styleErr.Render("✗ could not delete " + key + ": " + err.Error())
		return m, nil
	}
	i := m.page*perPage + m.cursor
	m.rows = append(m.rows[:i:i], m.rows[i+1:]...)
	m.status = styleOK.Render("✓ Deleted " + key)
	if len(m.rows) == 0 {
		m.status += styleMuted.Render(" · no payloads left")
		return m, tea.Quit
	}
	m.page = min(m.page, pageCount(len(m.rows))-1)
	m.cursor = min(m.cursor, m.onPage()-1)
	return m, nil
}

func (m tableModel) View() string {
	if m.chosen != "" {
		return ""
	}
	if len(m.rows) == 0 {
		return m.status + "\n"
	}
	if m.confirmDrop != "" {
		m.status = styleWarn.Render("Delete "+m.confirmDrop+"? ") + styleMuted.Render(keyLabel("table", "confirm_delete")+" to delete · any other key cancels")
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
	l := func(action string) string { return keyLabel("table", action) }
	help := styleMuted.Render(fmt.Sprintf("%s/%s page · %s/%s select · %s view · %s copy · %s delete · %s/%s first/last · %s quit · ",
		l("prev_page"), l("next_page"), l("up"), l("down"), l("view"), l("copy"), l("delete"), l("first"), l("last"), l("quit"))) + credit()
	return tableHeader(m.rows, m.page) + "\n" +
		renderPage(m.rows, m.page, m.cursor) + "\n" +
		"  " + pager + "   " + help + "\n" +
		m.status + "\n"
}
