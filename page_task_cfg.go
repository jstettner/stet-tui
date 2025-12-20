package main

import (
	"database/sql"
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

/**
 * TaskDefinition domain
 */

// TaskDefinition represents a task definition in the configuration page.
type TaskDefinition struct {
	id          string
	title       string
	description string
	active      bool
}

func (t TaskDefinition) FilterValue() string { return t.title }
func (t TaskDefinition) Title() string       { return t.title }
func (t TaskDefinition) Description() string { return t.description }

/**
 * Message types for task configuration
 */

// taskDefinitionsLoadedMsg contains task definitions loaded from DB.
type taskDefinitionsLoadedMsg struct {
	tasks []TaskDefinition
}

// taskDefinitionsLoadFailedMsg indicates loading task definitions failed.
type taskDefinitionsLoadFailedMsg struct {
	err error
}

// taskAddedMsg indicates a task was successfully added.
type taskAddedMsg struct {
	task TaskDefinition
}

// taskAddFailedMsg indicates adding a task failed.
type taskAddFailedMsg struct {
	err error
}

// taskActiveToggledMsg indicates the active status was toggled.
type taskActiveToggledMsg struct {
	taskID string
	active bool
}

// taskActiveToggleFailedMsg indicates toggling active status failed.
type taskActiveToggleFailedMsg struct {
	taskID string
	active bool
	err    error
}

// taskDeletedMsg indicates a task was soft-deleted.
type taskDeletedMsg struct {
	taskID string
}

// taskDeleteFailedMsg indicates soft-delete failed.
type taskDeleteFailedMsg struct {
	taskID string
	err    error
}

// InvalidateTodayPageMsg signals AppModel to reset Today page's initialized state.
type InvalidateTodayPageMsg struct{}

/**
 * Database commands
 */

// loadTaskDefinitionsCmd queries all non-deleted task definitions.
func loadTaskDefinitionsCmd(db *sql.DB) tea.Cmd {
	return func() tea.Msg {
		rows, err := db.Query(`
			SELECT id, title, description, active
			FROM task_definitions
			WHERE deleted = false
			ORDER BY created_at ASC
		`)
		if err != nil {
			return taskDefinitionsLoadFailedMsg{err: err}
		}
		defer rows.Close()

		var tasks []TaskDefinition
		for rows.Next() {
			var t TaskDefinition
			if err := rows.Scan(&t.id, &t.title, &t.description, &t.active); err != nil {
				return taskDefinitionsLoadFailedMsg{err: err}
			}
			tasks = append(tasks, t)
		}
		if err := rows.Err(); err != nil {
			return taskDefinitionsLoadFailedMsg{err: err}
		}
		return taskDefinitionsLoadedMsg{tasks: tasks}
	}
}

// addTaskDefinitionCmd inserts a new task definition.
func addTaskDefinitionCmd(db *sql.DB, title, description string) tea.Cmd {
	return func() tea.Msg {
		var id string
		err := db.QueryRow(`
			INSERT INTO task_definitions (id, title, description, active)
			VALUES (lower(hex(randomblob(16))), ?, ?, true)
			RETURNING id
		`, title, description).Scan(&id)
		if err != nil {
			return taskAddFailedMsg{err: err}
		}
		return taskAddedMsg{task: TaskDefinition{
			id:          id,
			title:       title,
			description: description,
			active:      true,
		}}
	}
}

// toggleTaskActiveCmd toggles the active status of a task definition.
func toggleTaskActiveCmd(db *sql.DB, taskID string, newActive bool) tea.Cmd {
	return func() tea.Msg {
		_, err := db.Exec(`
			UPDATE task_definitions SET active = ? WHERE id = ?
		`, newActive, taskID)
		if err != nil {
			return taskActiveToggleFailedMsg{taskID: taskID, active: newActive, err: err}
		}
		return taskActiveToggledMsg{taskID: taskID, active: newActive}
	}
}

// softDeleteTaskCmd sets deleted=true for a task definition.
func softDeleteTaskCmd(db *sql.DB, taskID string) tea.Cmd {
	return func() tea.Msg {
		_, err := db.Exec(`
			UPDATE task_definitions SET deleted = true WHERE id = ?
		`, taskID)
		if err != nil {
			return taskDeleteFailedMsg{taskID: taskID, err: err}
		}
		return taskDeletedMsg{taskID: taskID}
	}
}

/**
 * Task config delegate with active/inactive rendering
 */

// taskCfgDelegate renders task definitions with active/inactive indicator.
type taskCfgDelegate struct {
	list.DefaultDelegate
}

func (d *taskCfgDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	t, ok := item.(TaskDefinition)
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

	// Visual indicator: checkmark for active, circle for inactive
	indicator := "✓"
	indicatorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#04B575"))
	if !t.active {
		indicator = "○"
		indicatorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))
	}

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

	// Prepend indicator to title
	title = indicatorStyle.Render(indicator) + " " + title

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

	// Dim inactive tasks
	if !t.active && !isSelected {
		title = lipgloss.NewStyle().Foreground(lipgloss.Color("#666666")).Render(title)
		desc = lipgloss.NewStyle().Foreground(lipgloss.Color("#555555")).Render(desc)
	}

	// Render title and description
	if d.ShowDescription {
		fmt.Fprintf(w, "%s\n%s", title, desc)
	} else {
		fmt.Fprint(w, title)
	}
}

func newTaskCfgDelegate() *taskCfgDelegate {
	return &taskCfgDelegate{DefaultDelegate: list.NewDefaultDelegate()}
}

/**
 * TaskCfgPage implements the Page interface
 */

// taskCfgMode determines the current interaction state.
type taskCfgMode int

const (
	taskCfgModeList taskCfgMode = iota
	taskCfgModeAddTitle
	taskCfgModeAddDesc
	taskCfgModeConfirmDelete
)

// TaskCfgPage manages task definitions.
type TaskCfgPage struct {
	list list.Model
	db   *sql.DB
	mode taskCfgMode

	// Input fields for adding tasks
	titleInput textinput.Model
	descInput  textinput.Model

	// For delete confirmation
	pendingDeleteID    string
	pendingDeleteTitle string

	width  int
	height int
}

// NewTaskCfgPage creates and initializes the Task Configuration page.
func NewTaskCfgPage(db *sql.DB) *TaskCfgPage {
	delegate := newTaskCfgDelegate()
	l := list.New([]list.Item{}, delegate, 0, 0)
	l.Title = "Task Definitions"
	l.SetShowHelp(true)

	// Additional key bindings shown in help
	l.AdditionalShortHelpKeys = func() []key.Binding {
		return []key.Binding{
			key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "add")),
			key.NewBinding(key.WithKeys(" "), key.WithHelp("space", "toggle")),
			key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete")),
		}
	}

	// Title input
	ti := textinput.New()
	ti.Placeholder = "Task title..."
	ti.CharLimit = 100

	// Description input
	di := textinput.New()
	di.Placeholder = "Description (optional, press enter to skip)..."
	di.CharLimit = 200

	return &TaskCfgPage{
		list:       l,
		db:         db,
		mode:       taskCfgModeList,
		titleInput: ti,
		descInput:  di,
	}
}

func (p *TaskCfgPage) ID() PageID {
	return TaskCfgPageID
}

// CapturesNavigation returns true when the page is in a mode that needs
// to capture left/right arrow keys (e.g., text input).
func (p *TaskCfgPage) CapturesNavigation() bool {
	return p.mode != taskCfgModeList
}

func (p *TaskCfgPage) Title() title {
	return title{
		text:  "Configure",
		color: lipgloss.Color("#FF6B6B"),
	}
}

func (p *TaskCfgPage) SetSize(width, height int) {
	p.width = width
	p.height = height
	contentWidth := max(width-docStyle.GetHorizontalFrameSize(), 0)
	contentHeight := max(height-docStyle.GetVerticalFrameSize()-4, 0)
	p.list.SetWidth(contentWidth)
	p.list.SetHeight(contentHeight)
	p.titleInput.Width = max(contentWidth-4, 0)
	p.descInput.Width = max(contentWidth-4, 0)
}

// InitCmd loads task definitions from database.
func (p *TaskCfgPage) InitCmd() tea.Cmd {
	return loadTaskDefinitionsCmd(p.db)
}

func (p *TaskCfgPage) Update(msg tea.Msg) (Page, tea.Cmd) {
	switch p.mode {
	case taskCfgModeAddTitle:
		return p.updateAddTitleMode(msg)
	case taskCfgModeAddDesc:
		return p.updateAddDescMode(msg)
	case taskCfgModeConfirmDelete:
		return p.updateConfirmDeleteMode(msg)
	}

	var cmds []tea.Cmd

	// List mode - handle list updates and key commands
	var listCmd tea.Cmd
	p.list, listCmd = p.list.Update(msg)
	if listCmd != nil {
		cmds = append(cmds, listCmd)
	}

	switch msg := msg.(type) {
	// Handle loaded data
	case taskDefinitionsLoadedMsg:
		items := make([]list.Item, len(msg.tasks))
		for i, t := range msg.tasks {
			items[i] = t
		}
		p.list.SetItems(items)

	case taskDefinitionsLoadFailedMsg:
		cmds = append(cmds, p.list.NewStatusMessage(fmt.Sprintf("load failed: %v", msg.err)))

	// Handle add success
	case taskAddedMsg:
		items := p.list.Items()
		items = append(items, msg.task)
		p.list.SetItems(items)
		cmds = append(cmds, p.list.NewStatusMessage("Task added"))
		cmds = append(cmds, func() tea.Msg { return InvalidateTodayPageMsg{} })

	case taskAddFailedMsg:
		cmds = append(cmds, p.list.NewStatusMessage(fmt.Sprintf("add failed: %v", msg.err)))

	// Handle toggle success
	case taskActiveToggledMsg:
		statusMsg := "deactivated"
		if msg.active {
			statusMsg = "activated"
		}
		cmds = append(cmds, p.list.NewStatusMessage(statusMsg))
		cmds = append(cmds, func() tea.Msg { return InvalidateTodayPageMsg{} })

	// Handle toggle failure - rollback
	case taskActiveToggleFailedMsg:
		for i, item := range p.list.Items() {
			if t, ok := item.(TaskDefinition); ok && t.id == msg.taskID {
				t.active = !msg.active // Rollback
				p.list.SetItem(i, t)
				break
			}
		}
		cmds = append(cmds, p.list.NewStatusMessage(fmt.Sprintf("toggle failed: %v", msg.err)))

	// Handle delete success
	case taskDeletedMsg:
		items := p.list.Items()
		for i, item := range items {
			if t, ok := item.(TaskDefinition); ok && t.id == msg.taskID {
				items = append(items[:i], items[i+1:]...)
				break
			}
		}
		p.list.SetItems(items)
		cmds = append(cmds, p.list.NewStatusMessage("Task deleted"))
		cmds = append(cmds, func() tea.Msg { return InvalidateTodayPageMsg{} })

	case taskDeleteFailedMsg:
		cmds = append(cmds, p.list.NewStatusMessage(fmt.Sprintf("delete failed: %v", msg.err)))

	// Key handling
	case tea.KeyMsg:
		if p.list.SettingFilter() {
			break // Don't intercept when filtering
		}

		switch msg.String() {
		case "a": // Add new task
			p.mode = taskCfgModeAddTitle
			p.titleInput.Reset()
			p.titleInput.Focus()
			return p, textinput.Blink

		case " ": // Toggle active (space key)
			idx := p.list.Index()
			if idx < 0 || idx >= len(p.list.Items()) {
				break
			}
			item, ok := p.list.Items()[idx].(TaskDefinition)
			if !ok {
				break
			}
			// Optimistic update
			item.active = !item.active
			p.list.SetItem(idx, item)
			cmds = append(cmds, toggleTaskActiveCmd(p.db, item.id, item.active))

		case "d": // Delete task
			idx := p.list.Index()
			if idx < 0 || idx >= len(p.list.Items()) {
				break
			}
			item, ok := p.list.Items()[idx].(TaskDefinition)
			if !ok {
				break
			}
			p.pendingDeleteID = item.id
			p.pendingDeleteTitle = item.title
			p.mode = taskCfgModeConfirmDelete
		}
	}

	return p, tea.Batch(cmds...)
}

func (p *TaskCfgPage) updateAddTitleMode(msg tea.Msg) (Page, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			p.mode = taskCfgModeList
			return p, nil
		case "enter":
			if strings.TrimSpace(p.titleInput.Value()) == "" {
				return p, nil // Don't proceed with empty title
			}
			p.mode = taskCfgModeAddDesc
			p.descInput.Reset()
			p.descInput.Focus()
			return p, textinput.Blink
		}
	}

	var cmd tea.Cmd
	p.titleInput, cmd = p.titleInput.Update(msg)
	return p, cmd
}

func (p *TaskCfgPage) updateAddDescMode(msg tea.Msg) (Page, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			p.mode = taskCfgModeList
			return p, nil
		case "enter":
			title := strings.TrimSpace(p.titleInput.Value())
			desc := strings.TrimSpace(p.descInput.Value())
			p.mode = taskCfgModeList
			return p, addTaskDefinitionCmd(p.db, title, desc)
		}
	}

	var cmd tea.Cmd
	p.descInput, cmd = p.descInput.Update(msg)
	return p, cmd
}

func (p *TaskCfgPage) updateConfirmDeleteMode(msg tea.Msg) (Page, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "y", "Y":
			taskID := p.pendingDeleteID
			p.pendingDeleteID = ""
			p.pendingDeleteTitle = ""
			p.mode = taskCfgModeList
			return p, softDeleteTaskCmd(p.db, taskID)
		case "n", "N", "esc":
			p.pendingDeleteID = ""
			p.pendingDeleteTitle = ""
			p.mode = taskCfgModeList
		}
	}
	return p, nil
}

func (p *TaskCfgPage) View() string {
	switch p.mode {
	case taskCfgModeAddTitle:
		return p.viewAddTitle()
	case taskCfgModeAddDesc:
		return p.viewAddDesc()
	case taskCfgModeConfirmDelete:
		return p.viewConfirmDelete()
	}
	return p.list.View()
}

func (p *TaskCfgPage) viewAddTitle() string {
	return fmt.Sprintf(
		"Add New Task\n\nTitle:\n%s\n\n(enter to continue, esc to cancel)",
		p.titleInput.View(),
	)
}

func (p *TaskCfgPage) viewAddDesc() string {
	return fmt.Sprintf(
		"Add New Task\n\nTitle: %s\n\nDescription:\n%s\n\n(enter to save, esc to cancel)",
		p.titleInput.Value(),
		p.descInput.View(),
	)
}

func (p *TaskCfgPage) viewConfirmDelete() string {
	return fmt.Sprintf(
		"Delete Task\n\nAre you sure you want to delete \"%s\"?\n\n(y to confirm, n or esc to cancel)",
		p.pendingDeleteTitle,
	)
}
