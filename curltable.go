package main

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
)

// payload curl table: saved curl requests as a blue, paged table. It moves, copies and deletes with
// the "table" key bindings; "curl_table" adds run, edit (set) and add.

var (
	curlBlue       = lipgloss.AdaptiveColor{Light: "#1D4ED8", Dark: "#60A5FA"}
	styleCurlBadge = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#07172A")).Background(lipgloss.Color("#60A5FA")).Padding(0, 1)
	styleCurl      = lipgloss.NewStyle().Foreground(curlBlue).Bold(true)
)

type curlRow struct{ name, command, method, url string }

func loadCurlRows() []curlRow {
	vals := mustLoadCurls()
	rows := make([]curlRow, 0, len(vals))
	for _, n := range envNames(vals) {
		r := curlRow{name: n, command: vals[n], method: "?", url: previewValue(vals[n], 50)}
		if words, err := splitCommand(vals[n]); err == nil {
			r.method, r.url = describeCurl(words)
		}
		rows = append(rows, r)
	}
	return rows
}

func renderCurlPage(rows []curlRow, page, selected int) string {
	start := page * perPage
	end := min(start+perPage, len(rows))

	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(curlBlue)).
		Headers("#", "NAME", "METHOD", "URL").
		StyleFunc(func(row, col int) lipgloss.Style {
			s := lipgloss.NewStyle().Padding(0, 1)
			switch {
			case row == table.HeaderRow:
				return s.Bold(true).Foreground(curlBlue)
			case row == selected:
				s = s.Bold(true).Foreground(lipgloss.Color("#07172A")).Background(lipgloss.Color("#60A5FA"))
			case col == 0:
				s = s.Foreground(muted)
			case col == 2:
				s = s.Foreground(curlBlue)
			case col == 3:
				s = s.Foreground(lipgloss.AdaptiveColor{Light: "#374151", Dark: "#C9D1D9"})
			}
			if col == 0 {
				s = s.Align(lipgloss.Right)
			}
			return s
		})
	for i := start; i < end; i++ {
		r := rows[i]
		t.Row(strconv.Itoa(i+1), r.name, r.method, previewValue(r.url, 50))
	}
	return t.Render()
}

func curlTableHeader(rows []curlRow, page int) string {
	return styleCurlBadge.Render("curl") + "  " + styleMuted.Render(
		fmt.Sprintf("%d saved · page %d of %d", len(rows), page+1, pageCount(len(rows))))
}

func curlTableCmd(args []string) {
	rows := loadCurlRows()
	page := 0
	if len(args) > 0 {
		n, err := strconv.Atoi(args[0])
		if err != nil || n < 1 || n > pageCount(len(rows)) {
			fail("page must be between 1 and %d", pageCount(len(rows)))
		}
		page = n - 1
	}

	if !interactive {
		if len(rows) == 0 {
			info("No saved curl requests yet. Run:  payload curl set <name>")
			return
		}
		fmt.Println(curlTableHeader(rows, page))
		fmt.Println(renderCurlPage(rows, page, -1))
		if page+1 < pageCount(len(rows)) {
			info("Next page:  payload curl table %d", page+2)
		}
		return
	}

	// The table closes for run / edit / add (they need the whole terminal), then reopens after an
	// edit or add at the same spot.
	m := curlTableModel{rows: rows, page: page}
	for {
		if len(m.rows) == 0 && m.action == "" {
			m.action = "add" // nothing to show yet: start by adding one
		} else {
			res, err := tea.NewProgram(m).Run()
			if err != nil {
				fail("%v", err)
			}
			m = res.(curlTableModel)
		}
		action, target := m.action, m.target
		m.action, m.target, m.status = "", "", ""
		switch action {
		case "run":
			curlRun(target, nil)
			return
		case "edit":
			if command, ok := readCurlCommand(target, m.current().command); ok {
				m.status = saveFromTable(target, command)
			} else {
				m.status = styleMuted.Render("Not changed.")
			}
		case "add":
			name, ok := askCurlName()
			if !ok {
				if len(m.rows) == 0 {
					return
				}
				m.status = styleMuted.Render("Nothing added.")
				break
			}
			if command, ok := readCurlCommand(name, mustLoadCurls()[name]); ok {
				m.status = saveFromTable(name, command)
			} else {
				m.status = styleMuted.Render("Nothing added.")
			}
		default:
			return // quit
		}
		m.rows = loadCurlRows()
		if len(m.rows) == 0 {
			return
		}
		m.page = min(m.page, pageCount(len(m.rows))-1)
		m.cursor = min(m.cursor, m.onPage()-1)
	}
}

// saveFromTable saves an edited command and returns the status line for the table.
func saveFromTable(name, command string) string {
	fixed, fixes, warnings, err := fixCurlCommand(command)
	if err != nil {
		return styleErr.Render("✗ not saved: " + err.Error())
	}
	if _, err := storeCurl(name, fixed); err != nil {
		return styleErr.Render("✗ could not save " + name + ": " + err.Error())
	}
	s := styleOK.Render("✓ Saved " + name)
	if len(fixes) > 0 {
		s += styleMuted.Render(" · fixed: " + strings.Join(fixes, "; "))
	}
	if len(warnings) > 0 {
		s += "\n" + styleWarn.Render("! "+strings.Join(warnings, "\n! "))
	}
	return s
}

// askCurlName asks for a new request name; false when cancelled.
func askCurlName() (string, bool) {
	var name string
	err := huh.NewForm(huh.NewGroup(huh.NewInput().
		Title("Name for the curl request").
		Description("Saved in " + curlPath() + " · run it with: payload curl <name>").
		Placeholder("e.g. risk_score").
		Validate(func(s string) error { return curlNameError(strings.TrimSpace(s)) }).
		Value(&name))).WithTheme(theme()).WithShowHelp(true).Run()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(name), true
}

type curlTableModel struct {
	rows   []curlRow
	page   int
	cursor int // index within the current page
	status string

	action, target string // what to do once the table closes: run, edit or add
	confirmDrop    string // name awaiting a y/N answer before it is deleted
}

func (m curlTableModel) Init() tea.Cmd { return nil }

func (m curlTableModel) onPage() int {
	return min(perPage, len(m.rows)-m.page*perPage)
}

func (m curlTableModel) current() curlRow {
	return m.rows[m.page*perPage+m.cursor]
}

func (m curlTableModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
	case pressed(k, "curl_table", "run"):
		m.action, m.target = "run", m.current().name
		return m, tea.Quit
	case pressed(k, "curl_table", "edit"):
		m.action, m.target = "edit", m.current().name
		return m, tea.Quit
	case pressed(k, "curl_table", "add"):
		m.action = "add"
		return m, tea.Quit
	case pressed(k, "table", "delete"):
		m.confirmDrop = m.current().name
	case pressed(k, "table", "copy"):
		r := m.current()
		args, _, missing, err := curlArgs(r.command)
		if err == nil {
			err = copyText("curl " + quoteWords(args))
		}
		if err != nil {
			m.status = styleErr.Render("✗ could not copy " + r.name + ": " + err.Error())
		} else {
			m.status = styleOK.Render("✓ Copied the " + r.name + " command to the clipboard")
			if len(missing) > 0 {
				m.status += styleWarn.Render(" · not set: {{" + strings.Join(missing, "}}, {{") + "}}")
			}
		}
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

// answerDrop deletes the request awaiting confirmation on a confirm_delete key; any other key cancels.
func (m curlTableModel) answerDrop(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	name := m.confirmDrop
	m.confirmDrop = ""
	if !pressed(k, "table", "confirm_delete") {
		m.status = styleMuted.Render("Not deleted.")
		return m, nil
	}
	vals, err := loadCurls()
	if err == nil {
		delete(vals, name)
		err = saveCurls(vals)
	}
	if err != nil {
		m.status = styleErr.Render("✗ could not delete " + name + ": " + err.Error())
		return m, nil
	}
	i := m.page*perPage + m.cursor
	m.rows = append(m.rows[:i:i], m.rows[i+1:]...)
	m.status = styleOK.Render("✓ Deleted " + name)
	if len(m.rows) == 0 {
		m.status += styleMuted.Render(" · no curl requests left")
		return m, tea.Quit
	}
	m.page = min(m.page, pageCount(len(m.rows))-1)
	m.cursor = min(m.cursor, m.onPage()-1)
	return m, nil
}

func (m curlTableModel) View() string {
	if len(m.rows) == 0 || m.action != "" {
		return m.status + "\n"
	}
	if m.confirmDrop != "" {
		m.status = styleWarn.Render("Delete "+m.confirmDrop+"? ") + styleMuted.Render(keyLabel("table", "confirm_delete")+" to delete · any other key cancels")
	}
	pages := pageCount(len(m.rows))
	dots := make([]string, pages)
	for i := range dots {
		if i == m.page {
			dots[i] = styleCurl.Render("●")
		} else {
			dots[i] = styleMuted.Render("○")
		}
	}
	pager := strings.Join(dots, " ")
	if pages > 12 {
		pager = styleCurl.Render(fmt.Sprintf("%d/%d", m.page+1, pages))
	}
	l := func(action string) string { return keyLabel("table", action) }
	c := func(action string) string { return keyLabel("curl_table", action) }
	help := styleMuted.Render(fmt.Sprintf("%s/%s page · %s/%s select · %s run · %s edit · %s add · %s copy · %s delete · %s quit",
		l("prev_page"), l("next_page"), l("up"), l("down"), c("run"), c("edit"), c("add"), l("copy"), l("delete"), l("quit")))
	return curlTableHeader(m.rows, m.page) + "\n" +
		renderCurlPage(m.rows, m.page, m.cursor) + "\n" +
		"  " + pager + "   " + help + "\n" +
		m.status + "\n"
}
