package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// editJSONTUI opens a multi-line editor (arrow keys, home/end, paste) prefilled with initial.
// It returns the edited text, or exits quietly when the user cancels.
func editJSONTUI(key, initial string) string {
	ta := textarea.New()
	ta.ShowLineNumbers = true
	ta.Prompt = ""
	ta.CharLimit = 0
	ta.MaxHeight = 0 // no line limit, so long payloads paste in full
	ta.MaxWidth = 0
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.FocusedStyle.LineNumber = styleMuted
	ta.FocusedStyle.CursorLineNumber = styleAccent
	ta.Placeholder = "Paste or type JSON here"
	ta.SetValue(initial)
	ta.Focus()

	m, err := tea.NewProgram(editorModel{key: key, ta: ta}, tea.WithAltScreen()).Run()
	if err != nil {
		fail("%v", err)
	}
	em := m.(editorModel)
	if !em.saved {
		fmt.Println(styleMuted.Render("Cancelled."))
		os.Exit(130)
	}
	return em.ta.Value()
}

type editorModel struct {
	key   string
	ta    textarea.Model
	saved bool
}

func (m editorModel) Init() tea.Cmd { return textarea.Blink }

func (m editorModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.ta.SetWidth(msg.Width)
		m.ta.SetHeight(max(3, msg.Height-3)) // header + footer + spacing
	case tea.KeyMsg:
		switch {
		case msg.String() == "ctrl+c" || pressed(msg, "editor", "cancel"):
			return m, tea.Quit
		case pressed(msg, "editor", "save"):
			if strings.TrimSpace(m.ta.Value()) == "" {
				return m, nil
			}
			m.saved = true
			return m, tea.Quit
		case msg.String() == "tab":
			m.ta.InsertString("  ")
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.ta, cmd = m.ta.Update(msg)
	return m, cmd
}

func (m editorModel) View() string {
	header := styleBadge.Render(m.key) + "  " + styleMuted.Render(
		fmt.Sprintf("line %d of %d", m.ta.Line()+1, m.ta.LineCount()))

	val := strings.TrimSpace(m.ta.Value())
	status := styleMuted.Render("empty")
	if val != "" {
		var v any
		if err := json.Unmarshal([]byte(val), &v); err != nil {
			status = styleWarn.Render("✗ " + err.Error())
		} else {
			status = styleOK.Render("✓ valid JSON")
		}
	}
	help := styleMuted.Render(keyLabel("editor", "save") + " save · " + keyLabel("editor", "cancel") +
		" cancel · arrows/home/end/pgup/pgdn move")
	return header + "\n" + m.ta.View() + "\n" + status + "   " + help
}
