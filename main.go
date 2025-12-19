package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/paginator"
	"github.com/charmbracelet/lipgloss"

	tea "github.com/charmbracelet/bubbletea"
)

type View int

const (
	TodoView View = iota
	NotesView
	viewCount
)

type model struct {
	paginator paginator.Model
	notes     []string
}

func newModel() model {
	p := paginator.New()
	p.Type = paginator.Dots
	p.ActiveDot = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "235", Dark: "252"}).Render("•")
	p.InactiveDot = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "250", Dark: "238"}).Render("•")
	p.SetTotalPages(int(viewCount))

	return model{
		paginator: p,
		notes:     []string{},
	}
}

/**
 * Views
 */

func (m model) currentView() View {
	v := View(m.paginator.Page)
	if v < 0 || v >= viewCount {
		panic("invalid page")
	}
	return v
}

func (m model) todoView() string {
	return "Todo View"
}

func (m model) notesView() string {
	return "Notes View"
}

/**
 * App Control
 */

func (m model) Init() tea.Cmd {
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			return m, tea.Quit
		}
	}

	m.paginator, cmd = m.paginator.Update(msg)
	return m, cmd
}

func (m model) View() string {
	var b strings.Builder
	switch m.currentView() {
	case TodoView:
		b.WriteString(m.todoView())
	case NotesView:
		b.WriteString(m.notesView())
	}
	b.WriteString("\n\n")
	b.WriteString(m.paginator.View())
	return b.String()
}

func main() {
	p := tea.NewProgram(newModel())
	if _, err := p.Run(); err != nil {
		fmt.Println("Error running program:", err)
		os.Exit(1)
	}
}
