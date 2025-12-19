package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/paginator"
	"github.com/charmbracelet/lipgloss"

	tea "github.com/charmbracelet/bubbletea"
)

var docStyle = lipgloss.NewStyle().Margin(1, 2).Height(20)

/**
 * Tasks
 */

type Task struct {
	id          string
	title       string
	description string
	completed   bool
}

func (t Task) FilterValue() string { return t.title }
func (t Task) Title() string       { return t.title }
func (t Task) Description() string { return t.description }

var tasks_initial = []list.Item{
	Task{id: "1", title: "Task 1", description: "Description 1"},
	Task{id: "2", title: "Task 2", description: "Description 2"},
	Task{id: "3", title: "Task 3", description: "Description 3"},
}

/**
 * Views
 */

type View int

type title struct {
	text  string
	color lipgloss.Color
}

const (
	TodayView View = iota
	HistoryView
	viewCount
)

var viewTitles = [viewCount]title{
	// TODO: make colors adaptive
	{text: "Today", color: lipgloss.Color("#04B575")},
	{text: "History", color: lipgloss.Color("12")},
}

/**
 * Model
 */

type todayModel struct {
	tasks list.Model
}

type model struct {
	paginator  paginator.Model
	todayModel todayModel
	width      int
	height     int
}

func newModel() model {
	p := paginator.New()
	p.Type = paginator.Dots
	p.ActiveDot = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "235", Dark: "252"}).Render("•")
	p.InactiveDot = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "250", Dark: "238"}).Render("•")
	p.SetTotalPages(int(viewCount))

	today := todayModel{
		tasks: list.New(tasks_initial, list.NewDefaultDelegate(), 0, docStyle.GetHeight()),
	}
	today.tasks.Title = "Tasks"

	return model{
		todayModel: today,
		paginator:  p,
	}
}

func (m model) currentView() View {
	v := View(m.paginator.Page)
	if v < 0 || v >= viewCount {
		panic("invalid page")
	}
	return v
}

func (m model) renderTitle() string {
	title := viewTitles[m.currentView()]
	return lipgloss.NewStyle().
		Background(title.color).
		Render(title.text)
}

func (m model) todayView() string {
	today := m.todayModel
	return docStyle.Render(today.tasks.View())
}

func (m model) historyView() string {
	return "History Contents (placeholder)"
}

/**
 * App Control
 */

func (m model) Init() tea.Cmd {
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.todayModel.tasks.SetWidth(m.width)
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		}
	}

	var tasks_cmd, page_cmd tea.Cmd

	m.todayModel.tasks, tasks_cmd = m.todayModel.tasks.Update(msg)
	m.paginator, page_cmd = m.paginator.Update(msg)

	return m, tea.Batch(tasks_cmd, page_cmd)
}

func (m model) View() string {
	var b strings.Builder

	// View title
	b.WriteString(m.renderTitle())

	b.WriteString("\n\n")

	// View contents
	switch m.currentView() {
	case TodayView:
		b.WriteString(m.todayView())
	case HistoryView:
		b.WriteString(m.historyView())
	}

	b.WriteString("\n\n")

	// View tab indicator
	paginatorView := m.paginator.View()
	if m.width > 0 {
		contentWidth := max(m.width-docStyle.GetHorizontalFrameSize(), 0)
		if contentWidth > 0 {
			paginatorView = lipgloss.PlaceHorizontal(contentWidth, lipgloss.Center, paginatorView)
		}
	}
	b.WriteString(paginatorView)
	return docStyle.Render(b.String())
}

func main() {
	p := tea.NewProgram(newModel())
	if _, err := p.Run(); err != nil {
		fmt.Println("Error running program:", err)
		os.Exit(1)
	}
}
