package main

import (
	"database/sql"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// ---------------------------------------------------------------------------
// HistoryTask domain
// ---------------------------------------------------------------------------

// HistoryTask represents a task with its completion history.
type HistoryTask struct {
	id          string
	title       string
	completions map[string]bool // key: "YYYY-MM-DD", value: true if completed
}

func (t HistoryTask) FilterValue() string { return t.title }
func (t HistoryTask) Title() string       { return t.title }
func (t HistoryTask) Description() string { return "" }

// ---------------------------------------------------------------------------
// Messages
// ---------------------------------------------------------------------------

// historyDataLoadedMsg contains tasks with their completion history.
type historyDataLoadedMsg struct {
	tasks []HistoryTask
}

// historyDataLoadFailedMsg indicates loading history data failed.
type historyDataLoadFailedMsg struct {
	err error
}

// historyCompletionSavedMsg indicates the completion toggle was saved.
type historyCompletionSavedMsg struct {
	taskID    string
	date      string
	completed bool
}

// historyCompletionSaveFailedMsg indicates the completion toggle failed.
type historyCompletionSaveFailedMsg struct {
	taskID    string
	date      string
	completed bool
	err       error
}

// ---------------------------------------------------------------------------
// Database commands
// ---------------------------------------------------------------------------

func loadHistoryDataCmd(db *sql.DB, daysToShow int) tea.Cmd {
	return func() tea.Msg {
		// Query 1: Get all active, non-deleted tasks
		taskRows, err := db.Query(`
			SELECT id, title
			FROM task_definitions
			WHERE active = true AND deleted = false
			ORDER BY created_at ASC
		`)
		if err != nil {
			return historyDataLoadFailedMsg{err: err}
		}
		defer taskRows.Close()

		var tasks []HistoryTask
		for taskRows.Next() {
			var t HistoryTask
			if err := taskRows.Scan(&t.id, &t.title); err != nil {
				return historyDataLoadFailedMsg{err: err}
			}
			t.completions = make(map[string]bool)
			tasks = append(tasks, t)
		}
		if err := taskRows.Err(); err != nil {
			return historyDataLoadFailedMsg{err: err}
		}

		// Build map after slice is fully populated (avoids pointer invalidation from append)
		taskMap := make(map[string]*HistoryTask)
		for i := range tasks {
			taskMap[tasks[i].id] = &tasks[i]
		}

		// Query 2: Get completions in date range
		// Use date() to ensure we get just the date portion (YYYY-MM-DD)
		histRows, err := db.Query(`
			SELECT task_id, date(completed_date)
			FROM task_history
			WHERE completed_date >= date('now', 'localtime', ?)
			  AND completed_date <= date('now', 'localtime')
		`, fmt.Sprintf("-%d days", daysToShow-1))
		if err != nil {
			return historyDataLoadFailedMsg{err: err}
		}
		defer histRows.Close()

		for histRows.Next() {
			var taskID, date string
			if err := histRows.Scan(&taskID, &date); err != nil {
				return historyDataLoadFailedMsg{err: err}
			}
			if task, exists := taskMap[taskID]; exists {
				task.completions[date] = true
			}
		}
		if err := histRows.Err(); err != nil {
			return historyDataLoadFailedMsg{err: err}
		}

		return historyDataLoadedMsg{tasks: tasks}
	}
}

func saveHistoryCompletionCmd(db *sql.DB, taskID, date string, completed bool) tea.Cmd {
	return func() tea.Msg {
		var err error
		if completed {
			_, err = db.Exec(`
				INSERT INTO task_history (id, task_id, completed_date)
				VALUES (lower(hex(randomblob(16))), ?, ?)
				ON CONFLICT(task_id, completed_date) DO NOTHING
			`, taskID, date)
		} else {
			_, err = db.Exec(`
				DELETE FROM task_history
				WHERE task_id = ? AND completed_date = ?
			`, taskID, date)
		}
		if err != nil {
			return historyCompletionSaveFailedMsg{taskID: taskID, date: date, completed: completed, err: err}
		}
		return historyCompletionSavedMsg{taskID: taskID, date: date, completed: completed}
	}
}

// ---------------------------------------------------------------------------
// Width calculation
// ---------------------------------------------------------------------------

const (
	minTitleWidth   = 20 // Minimum characters reserved for task title
	titleHeatmapGap = 2  // Space between title and heatmap
	histListPadding = 6  // Account for list.Model's internal padding/borders
	minDaysToShow   = 7
	maxDaysToShow   = 90
)

func calculateDaysToShow(terminalWidth int) int {
	// Available width after accounting for docStyle margins
	contentWidth := terminalWidth - docStyle.GetHorizontalFrameSize()

	// Width available for heatmap (each square = 1 character)
	heatmapWidth := contentWidth - minTitleWidth - titleHeatmapGap - histListPadding

	daysToShow := heatmapWidth
	if daysToShow < minDaysToShow {
		daysToShow = minDaysToShow
	}
	if daysToShow > maxDaysToShow {
		daysToShow = maxDaysToShow
	}

	return daysToShow
}

// ---------------------------------------------------------------------------
// History delegate
// ---------------------------------------------------------------------------

// Heatmap characters and styles
const (
	completedSquare = "■"
	missedSquare    = "□"
)

var (
	heatmapCompletedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#04B575"))
	heatmapMissedStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#3C3C3C"))
)

type historyDelegate struct {
	list.DefaultDelegate
	daysToShow   int
	dateRange    []string // Pre-computed list of date strings (newest to oldest)
	selectedCell int      // which cell to highlight
	selectedRow  int      // which row to highlight (matches list.Index())
}

func newHistoryDelegate(daysToShow int) *historyDelegate {
	d := &historyDelegate{
		DefaultDelegate: list.NewDefaultDelegate(),
		daysToShow:      daysToShow,
	}
	d.ShowDescription = false
	d.SetHeight(1)
	d.SetSpacing(0)
	d.generateDateRange()
	return d
}

func (d *historyDelegate) generateDateRange() {
	d.dateRange = make([]string, d.daysToShow)
	yesterday := time.Now().AddDate(0, 0, -1)
	for i := 0; i < d.daysToShow; i++ {
		// Most recent (yesterday) first (left), oldest last (right)
		date := yesterday.AddDate(0, 0, -i)
		d.dateRange[i] = date.Format("2006-01-02")
	}
}

func (d *historyDelegate) renderHeatmap(task HistoryTask, isSelectedRow bool) string {
	var b strings.Builder

	for i, date := range d.dateRange {
		completed := task.completions[date]
		var style lipgloss.Style
		if completed {
			style = heatmapCompletedStyle
		} else {
			style = heatmapMissedStyle
		}
		// Highlight selected cell on selected row
		if isSelectedRow && i == d.selectedCell {
			style = style.Underline(true)
		}
		if completed {
			b.WriteString(style.Render(completedSquare))
		} else {
			b.WriteString(style.Render(missedSquare))
		}
	}

	return b.String()
}

func (d *historyDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	task, ok := item.(HistoryTask)
	if !ok {
		return
	}

	if m.Width() <= 0 {
		return
	}

	s := &d.Styles
	isSelected := index == m.Index()

	// Calculate available width for title
	heatmapWidth := d.daysToShow
	availableWidth := m.Width() - s.NormalTitle.GetPaddingLeft() - s.NormalTitle.GetPaddingRight()
	titleWidth := availableWidth - heatmapWidth - titleHeatmapGap
	if titleWidth < minTitleWidth {
		titleWidth = minTitleWidth
	}

	// Truncate title if needed
	title := task.Title()
	titleLen := lipgloss.Width(title)
	if titleLen > titleWidth {
		title = ansi.Truncate(title, titleWidth-1, "…")
		titleLen = lipgloss.Width(title)
	}
	// Pad title to ensure heatmap alignment
	if titleLen < titleWidth {
		title = title + strings.Repeat(" ", titleWidth-titleLen)
	}

	// Render heatmap
	heatmap := d.renderHeatmap(task, isSelected)

	// Combine title and heatmap
	content := title + strings.Repeat(" ", titleHeatmapGap) + heatmap

	// Apply selection styling
	if isSelected {
		content = s.SelectedTitle.Render(content)
	} else {
		content = s.NormalTitle.Render(content)
	}

	fmt.Fprint(w, content)
}

// ---------------------------------------------------------------------------
// HistoryPage
// ---------------------------------------------------------------------------

// historyKeyMap defines key bindings for the History page.
type historyKeyMap struct {
	Earlier key.Binding
	Later   key.Binding
	Toggle  key.Binding
}

var historyKeys = historyKeyMap{
	Earlier: key.NewBinding(
		key.WithKeys("["),
		key.WithHelp("[", "earlier"),
	),
	Later: key.NewBinding(
		key.WithKeys("]"),
		key.WithHelp("]", "later"),
	),
	Toggle: key.NewBinding(
		key.WithKeys(" "),
		key.WithHelp("space", "toggle"),
	),
}

// HistoryPage displays historical task completion data.
type HistoryPage struct {
	list         list.Model
	delegate     *historyDelegate // direct reference for updating selection
	db           *sql.DB
	width        int
	height       int
	daysToShow   int
	selectedCell int // 0 = leftmost (newest), daysToShow-1 = rightmost (oldest)
}

// NewHistoryPage creates and initializes the History page.
func NewHistoryPage(db *sql.DB) *HistoryPage {
	// Default days until we get terminal width
	defaultDays := 30

	delegate := newHistoryDelegate(defaultDays)
	l := list.New([]list.Item{}, delegate, 0, 0)
	l.Title = "Completion History"
	l.SetShowHelp(false)
	l.SetFilteringEnabled(false)
	l.SetShowStatusBar(false)

	return &HistoryPage{
		list:         l,
		delegate:     delegate,
		db:           db,
		daysToShow:   defaultDays,
		selectedCell: 0,
	}
}

func (p *HistoryPage) ID() PageID {
	return HistoryPageID
}

func (p *HistoryPage) Title() title {
	return title{
		text:  "History",
		color: lipgloss.Color("12"),
	}
}

func (p *HistoryPage) SetSize(width, height int) {
	p.width = width
	p.height = height

	contentWidth := max(width-docStyle.GetHorizontalFrameSize(), 0)
	p.list.SetWidth(contentWidth)
	p.list.SetHeight(height)
}

func (p *HistoryPage) InitCmd() tea.Cmd {
	return loadHistoryDataCmd(p.db, p.daysToShow)
}

func (p *HistoryPage) Update(msg tea.Msg) (Page, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case historyDataLoadedMsg:
		items := make([]list.Item, len(msg.tasks))
		for i, t := range msg.tasks {
			items[i] = t
		}
		p.list.SetItems(items)

	case historyDataLoadFailedMsg:
		cmds = append(cmds, p.list.NewStatusMessage(
			fmt.Sprintf("load failed: %v", msg.err)))

	case historyCompletionSavedMsg:
		status := fmt.Sprintf("%s: marked incomplete", msg.date)
		if msg.completed {
			status = fmt.Sprintf("%s: marked completed", msg.date)
		}
		cmds = append(cmds, p.list.NewStatusMessage(status))

	case historyCompletionSaveFailedMsg:
		// Revert optimistic update
		for i, listItem := range p.list.Items() {
			task, ok := listItem.(HistoryTask)
			if !ok || task.id != msg.taskID {
				continue
			}
			task.completions[msg.date] = !msg.completed
			p.list.SetItem(i, task)
			break
		}
		cmds = append(cmds, p.list.NewStatusMessage(fmt.Sprintf("save failed: %v", msg.err)))

	case tea.WindowSizeMsg:
		// Recalculate days and reload if changed
		newDays := calculateDaysToShow(msg.Width)
		if newDays != p.daysToShow {
			p.daysToShow = newDays
			// Clamp selectedCell to new range
			if p.selectedCell >= newDays {
				p.selectedCell = newDays - 1
			}
			// Update delegate with new days
			delegate := newHistoryDelegate(newDays)
			delegate.selectedCell = p.selectedCell
			p.delegate = delegate
			p.list.SetDelegate(delegate)
			// Reload data for new date range
			cmds = append(cmds, loadHistoryDataCmd(p.db, p.daysToShow))
		}

	case tea.KeyMsg:
		switch {
		case key.Matches(msg, historyKeys.Earlier):
			if p.selectedCell > 0 {
				p.selectedCell--
				p.delegate.selectedCell = p.selectedCell
			}
			return p, nil // Consume key

		case key.Matches(msg, historyKeys.Later):
			if p.selectedCell < p.daysToShow-1 {
				p.selectedCell++
				p.delegate.selectedCell = p.selectedCell
			}
			return p, nil // Consume key

		case key.Matches(msg, historyKeys.Toggle):
			return p.handleSpaceToggle()
		}
	}

	// Let list handle navigation
	var listCmd tea.Cmd
	p.list, listCmd = p.list.Update(msg)
	if listCmd != nil {
		cmds = append(cmds, listCmd)
	}

	return p, tea.Batch(cmds...)
}

func (p *HistoryPage) handleSpaceToggle() (Page, tea.Cmd) {
	idx := p.list.Index()
	if idx < 0 || idx >= len(p.list.Items()) {
		return p, nil
	}

	item, ok := p.list.Items()[idx].(HistoryTask)
	if !ok {
		return p, nil
	}

	if p.selectedCell < 0 || p.selectedCell >= len(p.delegate.dateRange) {
		return p, nil
	}
	selectedDate := p.delegate.dateRange[p.selectedCell]

	// Toggle completion state (optimistic UI update)
	newCompleted := !item.completions[selectedDate]
	item.completions[selectedDate] = newCompleted

	// Update list item
	setCmd := p.list.SetItem(idx, item)

	// Persist to DB
	saveCmd := saveHistoryCompletionCmd(p.db, item.id, selectedDate, newCompleted)

	return p, tea.Batch(setCmd, saveCmd)
}

func (p *HistoryPage) View() string {
	return p.list.View()
}

func (p *HistoryPage) KeyMap() []key.Binding {
	return []key.Binding{
		historyKeys.Earlier,
		historyKeys.Later,
		historyKeys.Toggle,
	}
}
