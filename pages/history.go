package pages

import (
	"database/sql"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/viewport"
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
// JournalEntry domain
// ---------------------------------------------------------------------------

// JournalEntry represents a journal entry with its date and content.
type JournalEntry struct {
	id        string
	entryDate time.Time
	content   string
}

func (j JournalEntry) FilterValue() string { return j.entryDate.Format("2006-01-02") }
func (j JournalEntry) Title() string       { return j.entryDate.Format("2006-01-02") }
func (j JournalEntry) Description() string { return "" }

// ---------------------------------------------------------------------------
// History mode
// ---------------------------------------------------------------------------

type historyMode int

const (
	historyModeTaskTable historyMode = iota
	historyModeJournalTable
	historyModeJournalPager
)

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

// journalHistoryLoadedMsg contains all journal entries.
type journalHistoryLoadedMsg struct {
	entries []JournalEntry
}

// journalHistoryLoadFailedMsg indicates loading journal entries failed.
type journalHistoryLoadFailedMsg struct {
	err error
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

func loadJournalHistoryCmd(db *sql.DB) tea.Cmd {
	return func() tea.Msg {
		rows, err := db.Query(`
			SELECT id, entry_date, content
			FROM journal_entries
			ORDER BY entry_date DESC
		`)
		if err != nil {
			return journalHistoryLoadFailedMsg{err: err}
		}
		defer rows.Close()

		var entries []JournalEntry
		for rows.Next() {
			var e JournalEntry
			var dateStr string
			if err := rows.Scan(&e.id, &dateStr, &e.content); err != nil {
				return journalHistoryLoadFailedMsg{err: err}
			}
			var parseErr error
			e.entryDate, parseErr = time.Parse(time.RFC3339, dateStr)
			if parseErr != nil {
				// Fallback to date-only format
				e.entryDate, parseErr = time.Parse("2006-01-02", dateStr)
				if parseErr != nil {
					return journalHistoryLoadFailedMsg{err: fmt.Errorf("parse date %q: %w", dateStr, parseErr)}
				}
			}
			entries = append(entries, e)
		}
		if err := rows.Err(); err != nil {
			return journalHistoryLoadFailedMsg{err: err}
		}

		return journalHistoryLoadedMsg{entries: entries}
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
	// Available width after accounting for DocStyle margins
	contentWidth := terminalWidth - DocStyle.GetHorizontalFrameSize()

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
// Journal delegate
// ---------------------------------------------------------------------------

// journalDelegate renders journal entries showing only the date.
type journalDelegate struct {
	list.DefaultDelegate
}

func newJournalDelegate() *journalDelegate {
	d := &journalDelegate{
		DefaultDelegate: list.NewDefaultDelegate(),
	}
	d.ShowDescription = false
	d.SetHeight(1)
	d.SetSpacing(0)
	return d
}

func (d *journalDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	entry, ok := item.(JournalEntry)
	if !ok {
		return
	}

	if m.Width() <= 0 {
		return
	}

	s := &d.Styles
	isSelected := index == m.Index()

	// Format: "2006-01-02"
	dateStr := entry.entryDate.Format("2006-01-02")

	if isSelected {
		dateStr = s.SelectedTitle.Render(dateStr)
	} else {
		dateStr = s.NormalTitle.Render(dateStr)
	}

	fmt.Fprint(w, dateStr)
}

// ---------------------------------------------------------------------------
// HistoryPage
// ---------------------------------------------------------------------------

// historyKeyMap defines key bindings for the History page.
type historyKeyMap struct {
	Earlier     key.Binding
	Later       key.Binding
	Toggle      key.Binding
	SwitchTable key.Binding
	Enter       key.Binding
	Back        key.Binding
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
	SwitchTable: key.NewBinding(
		key.WithKeys("tab"),
		key.WithHelp("tab", "switch table"),
	),
	Enter: key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "view entries"),
	),
	Back: key.NewBinding(
		key.WithKeys("esc", "q"),
		key.WithHelp("esc/q", "back"),
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

	// Journal history fields
	mode            historyMode
	journalList     list.Model
	journalEntries  []JournalEntry
	thisYearEntry   string
	lastYearEntry   string
	twoYearsEntry string
	viewport        viewport.Model
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

	// Initialize journal list
	journalDelegate := newJournalDelegate()
	jl := list.New([]list.Item{}, journalDelegate, 0, 0)
	jl.Title = "Journal History"
	jl.SetShowHelp(false)
	jl.SetFilteringEnabled(false)
	jl.SetShowStatusBar(false)

	return &HistoryPage{
		list:         l,
		delegate:     delegate,
		db:           db,
		daysToShow:   defaultDays,
		selectedCell: 0,
		mode:         historyModeTaskTable,
		journalList:  jl,
	}
}

func (p *HistoryPage) ID() PageID {
	return HistoryPageID
}

func (p *HistoryPage) Title() Title {
	return Title{
		Text:  "History",
		Color: lipgloss.Color("12"),
	}
}

func (p *HistoryPage) SetSize(width, height int) {
	p.width = width
	p.height = height

	contentWidth := max(width-DocStyle.GetHorizontalFrameSize(), 0)

	// Calculate heights for each section
	taskHeight, journalHeight := p.calculateHeights()

	p.list.SetWidth(contentWidth)
	p.list.SetHeight(taskHeight)

	p.journalList.SetWidth(contentWidth)
	p.journalList.SetHeight(journalHeight)

	// Update viewport for pager mode
	p.viewport.Width = contentWidth
	p.viewport.Height = height - 4 // -4 for header and scroll indicator
}

func (p *HistoryPage) calculateHeights() (taskHeight, journalHeight int) {
	// Journal table gets fixed 5 rows + 2 for title/padding
	journalHeight = 7

	// Comparison boxes: 3 boxes × 4 lines each = 12
	boxesHeight := 12

	// Overhead: divider (2 lines with newlines) + newlines between sections
	overhead := 4

	// Task table gets all remaining space
	taskHeight = p.height - journalHeight - boxesHeight - overhead
	if taskHeight < 5 {
		taskHeight = 5
	}

	return
}

func (p *HistoryPage) InitCmd() tea.Cmd {
	return tea.Batch(
		loadHistoryDataCmd(p.db, p.daysToShow),
		loadJournalHistoryCmd(p.db),
	)
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

	case journalHistoryLoadedMsg:
		p.journalEntries = msg.entries
		items := make([]list.Item, len(msg.entries))
		for i, e := range msg.entries {
			items[i] = e
		}
		p.journalList.SetItems(items)
		if len(items) > 0 {
			p.updateComparisonBoxes()
		}

	case journalHistoryLoadFailedMsg:
		cmds = append(cmds, p.journalList.NewStatusMessage(
			fmt.Sprintf("journal load failed: %v", msg.err)))

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
		// Mode-specific key handling
		switch p.mode {
		case historyModeJournalPager:
			return p.handlePagerKeys(msg)
		case historyModeJournalTable:
			return p.handleJournalTableKeys(msg)
		default:
			return p.handleTaskTableKeys(msg)
		}
	}

	// Let appropriate list handle navigation based on mode
	var listCmd tea.Cmd
	switch p.mode {
	case historyModeJournalTable:
		prevIndex := p.journalList.Index()
		p.journalList, listCmd = p.journalList.Update(msg)
		if p.journalList.Index() != prevIndex {
			p.updateComparisonBoxes()
		}
	case historyModeJournalPager:
		p.viewport, listCmd = p.viewport.Update(msg)
	default:
		p.list, listCmd = p.list.Update(msg)
	}
	if listCmd != nil {
		cmds = append(cmds, listCmd)
	}

	return p, tea.Batch(cmds...)
}

func (p *HistoryPage) handleTaskTableKeys(msg tea.KeyMsg) (Page, tea.Cmd) {
	switch {
	case key.Matches(msg, historyKeys.Earlier):
		if p.selectedCell > 0 {
			p.selectedCell--
			p.delegate.selectedCell = p.selectedCell
		}
		return p, nil

	case key.Matches(msg, historyKeys.Later):
		if p.selectedCell < p.daysToShow-1 {
			p.selectedCell++
			p.delegate.selectedCell = p.selectedCell
		}
		return p, nil

	case key.Matches(msg, historyKeys.Toggle):
		return p.handleSpaceToggle()

	case key.Matches(msg, historyKeys.SwitchTable):
		p.mode = historyModeJournalTable
		return p, nil
	}

	// Check for j/down at last item to switch to journal list
	if msg.String() == "j" || msg.String() == "down" {
		if p.list.Index() == len(p.list.Items())-1 {
			p.mode = historyModeJournalTable
			return p, nil
		}
	}

	// Let list handle other navigation
	var listCmd tea.Cmd
	p.list, listCmd = p.list.Update(msg)
	return p, listCmd
}

func (p *HistoryPage) handleJournalTableKeys(msg tea.KeyMsg) (Page, tea.Cmd) {
	switch {
	case key.Matches(msg, historyKeys.SwitchTable):
		p.mode = historyModeTaskTable
		return p, nil

	case key.Matches(msg, historyKeys.Enter):
		if len(p.journalList.Items()) > 0 {
			p.openPagerView()
		}
		return p, nil
	}

	// Check for k/up at first item to switch to task list
	if msg.String() == "k" || msg.String() == "up" {
		if p.journalList.Index() == 0 {
			p.mode = historyModeTaskTable
			return p, nil
		}
	}

	// Let journal list handle navigation
	var listCmd tea.Cmd
	prevIndex := p.journalList.Index()
	p.journalList, listCmd = p.journalList.Update(msg)
	if p.journalList.Index() != prevIndex {
		p.updateComparisonBoxes()
	}
	return p, listCmd
}

func (p *HistoryPage) handlePagerKeys(msg tea.KeyMsg) (Page, tea.Cmd) {
	if key.Matches(msg, historyKeys.Back) {
		p.mode = historyModeJournalTable
		return p, nil
	}

	// Let viewport handle navigation
	var cmd tea.Cmd
	p.viewport, cmd = p.viewport.Update(msg)
	return p, cmd
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

// ---------------------------------------------------------------------------
// Journal comparison boxes
// ---------------------------------------------------------------------------

func (p *HistoryPage) getSelectedJournalDate() time.Time {
	idx := p.journalList.Index()
	if idx < 0 || idx >= len(p.journalEntries) {
		return time.Now()
	}
	return p.journalEntries[idx].entryDate
}

func (p *HistoryPage) updateComparisonBoxes() {
	selectedDate := p.getSelectedJournalDate()

	// Clear existing
	p.thisYearEntry = ""
	p.lastYearEntry = ""
	p.twoYearsEntry = ""

	thisYear := selectedDate.Year()
	lastYear := thisYear - 1
	twoYearsAgo := thisYear - 2

	month := selectedDate.Month()
	day := selectedDate.Day()

	for _, entry := range p.journalEntries {
		if entry.entryDate.Month() == month && entry.entryDate.Day() == day {
			switch entry.entryDate.Year() {
			case thisYear:
				p.thisYearEntry = entry.content
			case lastYear:
				p.lastYearEntry = entry.content
			case twoYearsAgo:
				p.twoYearsEntry = entry.content
			}
		}
	}
}

func (p *HistoryPage) renderComparisonBoxes() string {
	selectedDate := p.getSelectedJournalDate()
	thisYear := selectedDate.Year()

	boxWidth := p.width - DocStyle.GetHorizontalFrameSize() - 4
	if boxWidth < 20 {
		boxWidth = 20
	}

	// Fixed small height - just 2 lines of content
	boxHeight := 4 // 2 lines content + 2 for border

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#555555")).
		Width(boxWidth).
		Height(boxHeight)

	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#888888"))

	noEntryStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#555555")).
		Italic(true)

	boxes := []struct {
		title   string
		content string
	}{
		{fmt.Sprintf("This Year (%d)", thisYear), p.thisYearEntry},
		{fmt.Sprintf("Last Year (%d)", thisYear-1), p.lastYearEntry},
		{fmt.Sprintf("2 Years Ago (%d)", thisYear-2), p.twoYearsEntry},
	}

	var renderedBoxes []string
	for _, box := range boxes {
		content := box.content
		if content == "" {
			content = noEntryStyle.Render("No entry")
		} else {
			content = truncateContent(content, boxWidth-2, 2)
		}

		boxContent := titleStyle.Render(box.title) + "\n" + content
		renderedBoxes = append(renderedBoxes, boxStyle.Render(boxContent))
	}

	return lipgloss.JoinVertical(lipgloss.Left, renderedBoxes...)
}

func truncateContent(content string, width, maxLines int) string {
	lines := strings.Split(content, "\n")
	var result []string
	for i, line := range lines {
		if i >= maxLines {
			break
		}
		if len(line) > width {
			result = append(result, line[:width-3]+"...")
		} else {
			result = append(result, line)
		}
	}
	if len(lines) > maxLines && len(result) > 0 {
		result[len(result)-1] = "..."
	}
	return strings.Join(result, "\n")
}

// ---------------------------------------------------------------------------
// Pager view
// ---------------------------------------------------------------------------

func (p *HistoryPage) openPagerView() {
	p.mode = historyModeJournalPager

	contentWidth := p.width - DocStyle.GetHorizontalFrameSize()
	contentHeight := p.height - 4

	p.viewport = viewport.New(contentWidth, contentHeight)
	p.viewport.SetContent(p.buildPagerContent())
	p.viewport.GotoTop()
}

func (p *HistoryPage) buildPagerContent() string {
	selectedDate := p.getSelectedJournalDate()
	dayMonth := selectedDate.Format("January 2")

	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#04B575"))

	dividerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#555555"))

	// Collect all entries for this day/month across all years
	type yearEntry struct {
		year    int
		content string
	}
	var entries []yearEntry

	for _, entry := range p.journalEntries {
		if entry.entryDate.Month() == selectedDate.Month() &&
			entry.entryDate.Day() == selectedDate.Day() {
			entries = append(entries, yearEntry{
				year:    entry.entryDate.Year(),
				content: entry.content,
			})
		}
	}

	// Sort by year descending (newest first)
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].year > entries[j].year
	})

	if len(entries) == 0 {
		return "No journal entries for " + dayMonth
	}

	var b strings.Builder
	b.WriteString(titleStyle.Render(fmt.Sprintf("Journal Entries for %s", dayMonth)))
	b.WriteString("\n\n")

	for i, entry := range entries {
		if i > 0 {
			b.WriteString("\n")
			b.WriteString(dividerStyle.Render(strings.Repeat("─", 40)))
			b.WriteString("\n\n")
		}

		b.WriteString(titleStyle.Render(fmt.Sprintf("%d", entry.year)))
		b.WriteString("\n\n")
		b.WriteString(entry.content)
		b.WriteString("\n")
	}

	return b.String()
}

func (p *HistoryPage) viewPager() string {
	var b strings.Builder

	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#04B575"))

	hintStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#555555"))

	b.WriteString(headerStyle.Render("Journal Entry Viewer"))
	b.WriteString(" ")
	b.WriteString(hintStyle.Render("(press esc or q to return)"))
	b.WriteString("\n\n")

	b.WriteString(p.viewport.View())

	// Scroll indicator
	scrollPercent := int(p.viewport.ScrollPercent() * 100)
	scrollStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#555555"))
	b.WriteString("\n")
	b.WriteString(scrollStyle.Render(fmt.Sprintf("%d%%", scrollPercent)))

	return b.String()
}

// ---------------------------------------------------------------------------
// View and KeyMap
// ---------------------------------------------------------------------------

func (p *HistoryPage) View() string {
	if p.mode == historyModeJournalPager {
		return p.viewPager()
	}

	var b strings.Builder

	// Task history table
	b.WriteString(p.list.View())
	b.WriteString("\n")

	// Section divider
	dividerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#444444"))
	contentWidth := p.width - DocStyle.GetHorizontalFrameSize()
	b.WriteString(dividerStyle.Render(strings.Repeat("─", contentWidth)))
	b.WriteString("\n")

	// Journal list (title rendered by list component)
	b.WriteString(p.journalList.View())
	b.WriteString("\n")

	// Comparison boxes
	if len(p.journalEntries) > 0 {
		b.WriteString(p.renderComparisonBoxes())
	}

	return b.String()
}

func (p *HistoryPage) KeyMap() []key.Binding {
	switch p.mode {
	case historyModeJournalPager:
		return []key.Binding{
			historyKeys.Back,
		}
	case historyModeJournalTable:
		return []key.Binding{
			historyKeys.SwitchTable,
			historyKeys.Enter,
		}
	default:
		return []key.Binding{
			historyKeys.Earlier,
			historyKeys.Later,
			historyKeys.Toggle,
			historyKeys.SwitchTable,
		}
	}
}

// CapturesNavigation implements NavigationCapturer to prevent page switching in pager mode.
func (p *HistoryPage) CapturesNavigation() bool {
	return p.mode == historyModeJournalPager
}
