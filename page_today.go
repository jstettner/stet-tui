package main

import (
	"database/sql"
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

// Initial tasks for demonstration.
var tasksInitial = []list.Item{
	Task{id: "1", title: "Task 1", description: "Description 1"},
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
	db    *sql.DB
}

// NewTodayPage creates and initializes the Today page.
func NewTodayPage(db *sql.DB) *TodayPage {
	delegate := newTaskDelegate()
	tasks := list.New(tasksInitial, delegate, 0, docStyle.GetHeight())
	tasks.Title = "Hit List"

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
	// Account for docStyle margins when setting list width
	contentWidth := max(width-docStyle.GetHorizontalFrameSize(), 0)
	p.tasks.SetWidth(contentWidth)
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
		// Be robust: depending on terminal/platform, space can come through as
		// KeySpace or KeyRunes with a single ' ' rune.
		isSpace := msg.Type == tea.KeySpace ||
			(msg.Type == tea.KeyRunes && len(msg.Runes) == 1 && msg.Runes[0] == ' ')
		if !isSpace {
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

		// Update list synchronously - SetItem returns a cmd for filtering
		setCmd := p.tasks.SetItem(selectedIdx, item)
		if setCmd != nil {
			cmds = append(cmds, setCmd)
		}

		// Persist to DB asynchronously
		cmds = append(cmds, saveTaskCompletionCmd(p.db, item.id, item.completed))
	}

	return p, tea.Batch(cmds...)
}

func (p *TodayPage) View() string {
	return docStyle.Render(p.tasks.View())
}
