package ui

import (
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/hunchom/yum/internal/ui/theme"
)

type pickItem struct{ title, desc string }

func (p pickItem) Title() string       { return p.title }
func (p pickItem) Description() string { return p.desc }
func (p pickItem) FilterValue() string { return p.title }

type pickerModel struct {
	list     list.Model
	choice   string
	quitting bool
}

func (m pickerModel) Init() tea.Cmd { return nil }

func (m pickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc", "q":
			m.quitting = true
			return m, tea.Quit
		case "enter":
			if it, ok := m.list.SelectedItem().(pickItem); ok {
				m.choice = it.title
			}
			return m, tea.Quit
		}
	case tea.WindowSizeMsg:
		m.list.SetSize(msg.Width, msg.Height)
	}
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m pickerModel) View() string {
	if m.quitting {
		return ""
	}
	return m.list.View()
}

// Pick shows an interactive list and returns the selected title, or "" if cancelled.
func Pick(prompt string, options []struct{ Name, Desc string }) (string, error) {
	items := make([]list.Item, len(options))
	for i, o := range options {
		items[i] = pickItem{title: o.Name, desc: o.Desc}
	}
	delegate := list.NewDefaultDelegate()
	l := list.New(items, delegate, 60, 16)
	l.Title = prompt
	l.Styles.Title = theme.Header
	m := pickerModel{list: l}
	res, err := tea.NewProgram(m).Run()
	if err != nil {
		return "", err
	}
	return res.(pickerModel).choice, nil
}
