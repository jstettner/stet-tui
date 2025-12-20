package main

import (
	"database/sql"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/key"
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

func (t *Task) ToggleCompleted() {
	t.completed = !t.completed
}

/**
 * Task completion persistence messages
 */

// taskCompletionSavedMsg indicates the DB write succeeded.
type taskCompletionSavedMsg struct {
	taskID    string
	completed bool
}

// taskCompletionSaveFailedMsg indicates the DB write failed.
type taskCompletionSaveFailedMsg struct {
	taskID    string
	completed bool
	err       error
}

// saveTaskCompletionCmd persists the task completion state to the database.
// If completed is true, inserts a row into task_history for today.
// If completed is false, deletes the row for today.
func saveTaskCompletionCmd(db *sql.DB, taskID string, completed bool) tea.Cmd {
	return func() tea.Msg {
		var err error
		if completed {
			// Insert completion for today (ignore if already exists)
			_, err = db.Exec(`
				INSERT INTO task_history (id, task_id, completed_date)
				VALUES (lower(hex(randomblob(16))), ?, date('now', 'localtime'))
				ON CONFLICT(task_id, completed_date) DO NOTHING
			`, taskID)
		} else {
			// Remove completion for today
			_, err = db.Exec(`
				DELETE FROM task_history
				WHERE task_id = ? AND completed_date = date('now', 'localtime')
			`, taskID)
		}

		if err != nil {
			return taskCompletionSaveFailedMsg{
				taskID:    taskID,
				completed: completed,
				err:       err,
			}
		}
		return taskCompletionSavedMsg{
			taskID:    taskID,
			completed: completed,
		}
	}
}

// activeTasksLoadedMsg contains active tasks loaded from DB with completion status.
type activeTasksLoadedMsg struct {
	tasks []Task
}

// activeTasksLoadFailedMsg indicates loading active tasks failed.
type activeTasksLoadFailedMsg struct {
	err error
}

// loadTodayDataCmd loads active, non-deleted tasks and today's completions.
func loadTodayDataCmd(db *sql.DB) tea.Cmd {
	return func() tea.Msg {
		// Load active, non-deleted task definitions
		rows, err := db.Query(`
			SELECT id, title, description
			FROM task_definitions
			WHERE active = true AND deleted = false
			ORDER BY created_at ASC
		`)
		if err != nil {
			return activeTasksLoadFailedMsg{err: err}
		}
		defer rows.Close()

		var tasks []Task
		for rows.Next() {
			var t Task
			if err := rows.Scan(&t.id, &t.title, &t.description); err != nil {
				return activeTasksLoadFailedMsg{err: err}
			}
			tasks = append(tasks, t)
		}
		if err := rows.Err(); err != nil {
			return activeTasksLoadFailedMsg{err: err}
		}

		// Load today's completions
		compRows, err := db.Query(`
			SELECT task_id FROM task_history
			WHERE completed_date = date('now', 'localtime')
		`)
		if err != nil {
			return activeTasksLoadFailedMsg{err: err}
		}
		defer compRows.Close()

		completedIDs := make(map[string]bool)
		for compRows.Next() {
			var taskID string
			if err := compRows.Scan(&taskID); err != nil {
				return activeTasksLoadFailedMsg{err: err}
			}
			completedIDs[taskID] = true
		}
		if err := compRows.Err(); err != nil {
			return activeTasksLoadFailedMsg{err: err}
		}

		// Mark tasks as completed
		for i := range tasks {
			if completedIDs[tasks[i].id] {
				tasks[i].completed = true
			}
		}

		return activeTasksLoadedMsg{tasks: tasks}
	}
}

// sortTasksByCompletion moves incomplete tasks to the front, completed to the end.
// Uses stable sort to preserve creation order within each group.
func sortTasksByCompletion(tasks []Task) {
	sort.SliceStable(tasks, func(i, j int) bool {
		return !tasks[i].completed && tasks[j].completed
	})
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

// todayKeyMap defines key bindings for the Today page.
type todayKeyMap struct {
	Toggle key.Binding
}

var todayKeys = todayKeyMap{
	Toggle: key.NewBinding(
		key.WithKeys(" "),
		key.WithHelp("space", "toggle"),
	),
}

// TodayPage displays today's tasks.
type TodayPage struct {
	tasks list.Model
	db    *sql.DB
}

// NewTodayPage creates and initializes the Today page.
func NewTodayPage(db *sql.DB) *TodayPage {
	delegate := newTaskDelegate()
	tasks := list.New([]list.Item{}, delegate, 0, 0)
	tasks.Title = "Hit List"
	tasks.SetShowHelp(false)

	return &TodayPage{
		tasks: tasks,
		db:    db,
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
	contentWidth := max(width-docStyle.GetHorizontalFrameSize(), 0)
	p.tasks.SetWidth(contentWidth)
	p.tasks.SetHeight(height)
}

// InitCmd loads active tasks and today's completions from the database.
func (p *TodayPage) InitCmd() tea.Cmd {
	return loadTodayDataCmd(p.db)
}

func (p *TodayPage) Update(msg tea.Msg) (Page, tea.Cmd) {
	var cmds []tea.Cmd

	// First, let the list handle the message
	var listCmd tea.Cmd
	p.tasks, listCmd = p.tasks.Update(msg)
	if listCmd != nil {
		cmds = append(cmds, listCmd)
	}

	switch msg := msg.(type) {
	case activeTasksLoadedMsg:
		// Sort so incomplete tasks appear first
		sortTasksByCompletion(msg.tasks)
		items := make([]list.Item, len(msg.tasks))
		for i, t := range msg.tasks {
			items[i] = t
		}
		p.tasks.SetItems(items)

	case activeTasksLoadFailedMsg:
		cmds = append(cmds, p.tasks.NewStatusMessage(fmt.Sprintf("load failed: %v", msg.err)))

	case taskCompletionSavedMsg:
		// Show status message
		statusMsg := "marked incomplete"
		if msg.completed {
			statusMsg = "marked completed"
		}
		cmds = append(cmds, p.tasks.NewStatusMessage(statusMsg))

		// DB write succeeded - nothing to do, UI already updated optimistically

	case taskCompletionSaveFailedMsg:
		cmds = append(cmds, p.tasks.NewStatusMessage(fmt.Sprintf("save failed: %v", msg.err)))
		// DB write failed - revert the UI state and show error
		for i, listItem := range p.tasks.Items() {
			task, ok := listItem.(Task)
			if !ok {
				continue
			}
			if task.id == msg.taskID {
				// Revert: toggle back to the opposite of what we tried to save
				task.completed = !msg.completed
				setCmd := p.tasks.SetItem(i, task)
				if setCmd != nil {
					cmds = append(cmds, setCmd)
				}
				break
			}
		}
		cmds = append(cmds, p.tasks.NewStatusMessage(fmt.Sprintf("save failed: %v", msg.err)))

	case tea.KeyMsg:
		if !key.Matches(msg, todayKeys.Toggle) {
			break
		}

		// If the user is typing into the filter input, space should be treated as text.
		if p.tasks.SettingFilter() {
			break
		}

		// Toggle task completion synchronously in Update
		selectedIdx := p.tasks.GlobalIndex()
		if selectedIdx < 0 || selectedIdx >= len(p.tasks.Items()) {
			break
		}

		item, ok := p.tasks.Items()[selectedIdx].(Task)
		if !ok {
			break
		}

		// Toggle state (optimistic UI update)
		item.ToggleCompleted()

		// Check if filter is active
		isFiltered := p.tasks.FilterState() == list.Filtering ||
			p.tasks.FilterState() == list.FilterApplied

		if isFiltered {
			// Filter active - just update the single item without re-sorting
			// to preserve filter state (SetItems resets filter mapping)
			setCmd := p.tasks.SetItem(selectedIdx, item)
			if setCmd != nil {
				cmds = append(cmds, setCmd)
			}
		} else {
			// No filter - safe to re-sort and reset items
			allItems := p.tasks.Items()
			tasks := make([]Task, 0, len(allItems))
			for i, listItem := range allItems {
				if i == selectedIdx {
					tasks = append(tasks, item)
				} else {
					tasks = append(tasks, listItem.(Task))
				}
			}
			sortTasksByCompletion(tasks)

			sortedItems := make([]list.Item, len(tasks))
			for i, t := range tasks {
				sortedItems[i] = t
			}
			p.tasks.SetItems(sortedItems)
		}

		// Persist to DB asynchronously
		cmds = append(cmds, saveTaskCompletionCmd(p.db, item.id, item.completed))
	}

	return p, tea.Batch(cmds...)
}

func (p *TodayPage) View() string {
	return p.tasks.View()
}

func (p *TodayPage) KeyMap() []key.Binding {
	return []key.Binding{
		todayKeys.Toggle,
	}
}
