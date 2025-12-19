package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

/**
 * Task domain
 */

// Task represents a to-do item.
type Task struct {
	id          string
	title       string
	description string
	completed   bool
}

func (t Task) FilterValue() string { return t.title }
func (t Task) Title() string       { return t.title }
func (t Task) Description() string { return t.description }

// Initial tasks for demonstration.
var tasksInitial = []list.Item{
	Task{id: "1", title: "Task 1", description: "Description 1", completed: true},
	Task{id: "2", title: "Task 2", description: "Description 2"},
	Task{id: "3", title: "Task 3", description: "Description 3"},
}

/**
 * Task list delegate with checkbox rendering
 */

const ellipsis = "…"

// taskDelegate embeds list.DefaultDelegate and overrides Render to show a checkbox.
type taskDelegate struct {
	list.DefaultDelegate
}

func (d *taskDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	t, ok := item.(Task)
	if !ok {
		return
	}

	var (
		title, desc  string
		matchedRunes []int
		s            = &d.Styles
	)

	title = t.Title()
	desc = t.Description()

	if m.Width() <= 0 {
		return
	}

	// Determine checkbox glyph (filled box for completed, empty box for not)
	checkbox := "□"
	if t.completed {
		checkbox = "■"
	}

	// Calculate text width (same as default, no extra reservation needed since checkbox is prepended)
	textwidth := m.Width() - s.NormalTitle.GetPaddingLeft() - s.NormalTitle.GetPaddingRight()
	if textwidth < 1 {
		textwidth = 1
	}

	// Truncate title
	title = ansi.Truncate(title, textwidth, ellipsis)

	// Handle description if shown
	if d.ShowDescription {
		var lines []string
		for i, line := range strings.Split(desc, "\n") {
			if i >= d.Height()-1 {
				break
			}
			lines = append(lines, ansi.Truncate(line, textwidth, ellipsis))
		}
		desc = strings.Join(lines, "\n")
	}

	// Conditions
	var (
		isSelected  = index == m.Index()
		emptyFilter = m.FilterState() == list.Filtering && m.FilterValue() == ""
		isFiltered  = m.FilterState() == list.Filtering || m.FilterState() == list.FilterApplied
	)

	if isFiltered && index < len(m.VisibleItems()) {
		matchedRunes = m.MatchesForItem(index)
	}

	// Prepend checkbox to title so it appears inside the styled block (after the │ border)
	title = checkbox + " " + title

	// Apply styles based on state
	if emptyFilter {
		title = s.DimmedTitle.Render(title)
		desc = s.DimmedDesc.Render(desc)
	} else if isSelected && m.FilterState() != list.Filtering {
		if isFiltered {
			unmatched := s.SelectedTitle.Inline(true)
			matched := unmatched.Inherit(s.FilterMatch)
			title = lipgloss.StyleRunes(title, matchedRunes, matched, unmatched)
		}
		title = s.SelectedTitle.Render(title)
		desc = s.SelectedDesc.Render(desc)
	} else {
		if isFiltered {
			unmatched := s.NormalTitle.Inline(true)
			matched := unmatched.Inherit(s.FilterMatch)
			title = lipgloss.StyleRunes(title, matchedRunes, matched, unmatched)
		}
		title = s.NormalTitle.Render(title)
		desc = s.NormalDesc.Render(desc)
	}

	// Render title (with checkbox inside) and description
	if d.ShowDescription {
		fmt.Fprintf(w, "%s\n%s", title, desc)
	} else {
		fmt.Fprint(w, title)
	}
}

func newTaskDelegate() *taskDelegate {
	return &taskDelegate{DefaultDelegate: list.NewDefaultDelegate()}
}

/**
 * TodayPage implements the Page interface
 */

// TodayPage displays today's tasks.
type TodayPage struct {
	tasks list.Model
}

// NewTodayPage creates and initializes the Today page.
func NewTodayPage() *TodayPage {
	delegate := newTaskDelegate()
	tasks := list.New(tasksInitial, delegate, 0, docStyle.GetHeight())
	tasks.Title = "Tasks"

	return &TodayPage{
		tasks: tasks,
	}
}

func (p *TodayPage) ID() PageID {
	return TodayPageID
}

func (p *TodayPage) Title() title {
	return title{
		text:  "Today",
		color: lipgloss.Color("#04B575"),
	}
}

func (p *TodayPage) SetSize(width, height int) {
	// Account for docStyle margins when setting list width
	contentWidth := max(width-docStyle.GetHorizontalFrameSize(), 0)
	p.tasks.SetWidth(contentWidth)
}

func (p *TodayPage) Update(msg tea.Msg) (Page, tea.Cmd) {
	var cmd tea.Cmd
	p.tasks, cmd = p.tasks.Update(msg)
	return p, cmd
}

func (p *TodayPage) View() string {
	return docStyle.Render(p.tasks.View())
}
