package pages

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const journalDebounceInterval = 500 * time.Millisecond

// journalMode represents the current input mode.
type journalMode int

const (
	journalModeView      journalMode = iota // Basic view, page nav works
	journalModeVimNormal                    // Vim normal mode
	journalModeVimInsert                    // Vim insert mode
)

// Message types for journal operations.
type journalEntryLoadedMsg struct {
	id      string
	content string
}

type journalEntryLoadFailedMsg struct {
	err error
}

type journalEntrySavedMsg struct{}

type journalEntrySaveFailedMsg struct {
	err error
}

type journalDebounceTickMsg struct {
	version int
}

// journalKeyMap defines key bindings for the Journal page.
type journalKeyMap struct {
	VimMode key.Binding
	Edit    key.Binding
	Escape  key.Binding
	Nav     key.Binding
	Delete  key.Binding
}

var journalKeys = journalKeyMap{
	VimMode: key.NewBinding(
		key.WithKeys("ctrl+v"),
		key.WithHelp("ctrl+v", "vim mode"),
	),
	Edit: key.NewBinding(
		key.WithKeys("i"),
		key.WithHelp("i", "insert"),
	),
	Escape: key.NewBinding(
		key.WithKeys("esc"),
		key.WithHelp("esc", "normal"),
	),
	Nav: key.NewBinding(
		key.WithKeys("h", "j", "k", "l"),
		key.WithHelp("hjkl", "navigate"),
	),
	Delete: key.NewBinding(
		key.WithKeys("x", "d"),
		key.WithHelp("x/dd", "delete"),
	),
}

// JournalPage allows users to create and edit daily journal entries.
type JournalPage struct {
	db       *sql.DB
	textarea textarea.Model
	mode     journalMode

	entryID          string
	debounceVersion  int
	lastSavedContent string
	pendingSave      bool
	pendingKey       string // For multi-key sequences (gg, dd)

	width  int
	height int
	err    error
}

// NewJournalPage creates a new journal page.
func NewJournalPage(db *sql.DB) *JournalPage {
	ta := textarea.New()
	ta.Placeholder = "Start writing your journal entry..."
	ta.CharLimit = 0
	ta.ShowLineNumbers = false

	return &JournalPage{
		db:       db,
		textarea: ta,
		mode:     journalModeView,
	}
}

func (p *JournalPage) ID() PageID {
	return JournalPageID
}

func (p *JournalPage) Title() Title {
	return Title{
		Text:  "Journal",
		Color: lipgloss.Color("#00CED1"),
	}
}

func (p *JournalPage) SetSize(width, height int) {
	p.width = width
	p.height = height

	contentWidth := max(width-DocStyle.GetHorizontalFrameSize()-4, 40)
	contentHeight := max(height-6, 5)

	p.textarea.SetWidth(contentWidth)
	p.textarea.SetHeight(contentHeight)
}

func (p *JournalPage) InitCmd() tea.Cmd {
	return loadOrCreateJournalEntryCmd(p.db)
}

func (p *JournalPage) CapturesNavigation() bool {
	return p.mode != journalModeView
}

func (p *JournalPage) CapturesGlobalKeys() bool {
	return p.mode == journalModeVimInsert
}

func (p *JournalPage) KeyMap() []key.Binding {
	switch p.mode {
	case journalModeView:
		return []key.Binding{journalKeys.VimMode}
	case journalModeVimNormal:
		return []key.Binding{journalKeys.Nav, journalKeys.Edit, journalKeys.Delete, journalKeys.VimMode}
	case journalModeVimInsert:
		return []key.Binding{journalKeys.Escape}
	}
	return nil
}

func (p *JournalPage) Update(msg tea.Msg) (Page, tea.Cmd) {
	switch msg := msg.(type) {
	case journalEntryLoadedMsg:
		p.entryID = msg.id
		p.textarea.SetValue(msg.content)
		p.lastSavedContent = msg.content
		p.err = nil
		return p, nil

	case journalEntryLoadFailedMsg:
		p.err = msg.err
		return p, nil

	case journalEntrySavedMsg:
		p.pendingSave = false
		p.lastSavedContent = p.textarea.Value()
		return p, nil

	case journalEntrySaveFailedMsg:
		p.pendingSave = false
		p.err = msg.err
		return p, nil

	case journalDebounceTickMsg:
		if msg.version == p.debounceVersion && p.textarea.Value() != p.lastSavedContent {
			p.pendingSave = true
			return p, saveJournalEntryCmd(p.db, p.entryID, p.textarea.Value())
		}
		return p, nil

	case tea.KeyMsg:
		return p.handleKeyMsg(msg)
	}

	// In insert mode, forward non-key messages to textarea
	if p.mode == journalModeVimInsert {
		var taCmd tea.Cmd
		oldValue := p.textarea.Value()
		p.textarea, taCmd = p.textarea.Update(msg)

		var cmds []tea.Cmd
		if taCmd != nil {
			cmds = append(cmds, taCmd)
		}

		if p.textarea.Value() != oldValue {
			p.debounceVersion++
			cmds = append(cmds, startDebounceCmd(p.debounceVersion))
		}

		return p, tea.Batch(cmds...)
	}

	return p, nil
}

func (p *JournalPage) handleKeyMsg(msg tea.KeyMsg) (Page, tea.Cmd) {
	switch p.mode {
	case journalModeView:
		return p.handleViewMode(msg)
	case journalModeVimNormal:
		return p.handleVimNormalMode(msg)
	case journalModeVimInsert:
		return p.handleVimInsertMode(msg)
	}
	return p, nil
}

func (p *JournalPage) handleViewMode(msg tea.KeyMsg) (Page, tea.Cmd) {
	if msg.String() == "ctrl+v" {
		p.mode = journalModeVimNormal
		p.textarea.Focus()
		return p, textarea.Blink
	}
	return p, nil
}

func (p *JournalPage) handleVimNormalMode(msg tea.KeyMsg) (Page, tea.Cmd) {
	keyStr := msg.String()

	// Handle multi-key sequences
	if p.pendingKey == "g" {
		p.pendingKey = ""
		if keyStr == "g" {
			// gg - go to start
			p.textarea, _ = p.textarea.Update(tea.KeyMsg{Type: tea.KeyHome, Alt: true})
			return p, nil
		}
		// Invalid sequence, ignore
		return p, nil
	}
	if p.pendingKey == "d" {
		p.pendingKey = ""
		if keyStr == "d" {
			// dd - delete line
			p.deleteLine()
			return p, startDebounceCmd(p.debounceVersion)
		}
		// Invalid sequence, ignore
		return p, nil
	}

	switch keyStr {
	// Exit vim mode
	case "ctrl+v":
		p.mode = journalModeView
		p.textarea.Blur()
		// Save if modified
		if p.textarea.Value() != p.lastSavedContent {
			p.pendingSave = true
			return p, saveJournalEntryCmd(p.db, p.entryID, p.textarea.Value())
		}
		return p, nil

	// Navigation - update textarea synchronously
	case "h":
		p.textarea, _ = p.textarea.Update(tea.KeyMsg{Type: tea.KeyLeft})
		return p, nil
	case "j":
		p.textarea, _ = p.textarea.Update(tea.KeyMsg{Type: tea.KeyDown})
		return p, nil
	case "k":
		p.textarea, _ = p.textarea.Update(tea.KeyMsg{Type: tea.KeyUp})
		return p, nil
	case "l":
		p.textarea, _ = p.textarea.Update(tea.KeyMsg{Type: tea.KeyRight})
		return p, nil
	case "w":
		p.textarea, _ = p.textarea.Update(tea.KeyMsg{Type: tea.KeyRight, Alt: true})
		return p, nil
	case "b":
		p.textarea, _ = p.textarea.Update(tea.KeyMsg{Type: tea.KeyLeft, Alt: true})
		return p, nil
	case "0":
		p.textarea, _ = p.textarea.Update(tea.KeyMsg{Type: tea.KeyHome})
		return p, nil
	case "$":
		p.textarea, _ = p.textarea.Update(tea.KeyMsg{Type: tea.KeyEnd})
		return p, nil
	case "G":
		p.textarea, _ = p.textarea.Update(tea.KeyMsg{Type: tea.KeyEnd, Alt: true})
		return p, nil

	// Multi-key sequence starters
	case "g", "d":
		p.pendingKey = keyStr
		return p, nil

	// Delete character
	case "x":
		p.textarea, _ = p.textarea.Update(tea.KeyMsg{Type: tea.KeyDelete})
		p.debounceVersion++
		return p, startDebounceCmd(p.debounceVersion)

	// Mode entry - insert variants
	case "i":
		p.mode = journalModeVimInsert
		return p, nil
	case "I":
		p.textarea, _ = p.textarea.Update(tea.KeyMsg{Type: tea.KeyHome})
		p.mode = journalModeVimInsert
		return p, nil
	case "a":
		p.textarea, _ = p.textarea.Update(tea.KeyMsg{Type: tea.KeyRight})
		p.mode = journalModeVimInsert
		return p, nil
	case "A":
		p.textarea, _ = p.textarea.Update(tea.KeyMsg{Type: tea.KeyEnd})
		p.mode = journalModeVimInsert
		return p, nil

	// Open line
	case "o":
		p.textarea, _ = p.textarea.Update(tea.KeyMsg{Type: tea.KeyEnd})
		p.textarea, _ = p.textarea.Update(tea.KeyMsg{Type: tea.KeyEnter})
		p.mode = journalModeVimInsert
		p.debounceVersion++
		return p, startDebounceCmd(p.debounceVersion)
	case "O":
		p.textarea, _ = p.textarea.Update(tea.KeyMsg{Type: tea.KeyHome})
		p.textarea, _ = p.textarea.Update(tea.KeyMsg{Type: tea.KeyEnter})
		p.textarea, _ = p.textarea.Update(tea.KeyMsg{Type: tea.KeyUp})
		p.mode = journalModeVimInsert
		p.debounceVersion++
		return p, startDebounceCmd(p.debounceVersion)
	}

	return p, nil
}

func (p *JournalPage) handleVimInsertMode(msg tea.KeyMsg) (Page, tea.Cmd) {
	if msg.String() == "esc" {
		p.mode = journalModeVimNormal
		// Save if modified
		if p.textarea.Value() != p.lastSavedContent {
			p.pendingSave = true
			return p, saveJournalEntryCmd(p.db, p.entryID, p.textarea.Value())
		}
		return p, nil
	}

	// Pass through to textarea
	var taCmd tea.Cmd
	oldValue := p.textarea.Value()
	p.textarea, taCmd = p.textarea.Update(msg)

	var cmds []tea.Cmd
	if taCmd != nil {
		cmds = append(cmds, taCmd)
	}

	if p.textarea.Value() != oldValue {
		p.debounceVersion++
		cmds = append(cmds, startDebounceCmd(p.debounceVersion))
	}

	return p, tea.Batch(cmds...)
}

// deleteLine deletes the current line (dd command)
func (p *JournalPage) deleteLine() {
	// Go to start of line
	p.textarea, _ = p.textarea.Update(tea.KeyMsg{Type: tea.KeyHome})
	// Delete before cursor (clears from start)
	p.textarea, _ = p.textarea.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	// Delete after cursor (to end of line)
	p.textarea, _ = p.textarea.Update(tea.KeyMsg{Type: tea.KeyCtrlK})
	// Delete the newline character if present
	p.textarea, _ = p.textarea.Update(tea.KeyMsg{Type: tea.KeyDelete})
	p.debounceVersion++
}

func (p *JournalPage) View() string {
	var b strings.Builder

	modeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#888888"))
	errorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#FF6B6B"))
	statusStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))

	today := time.Now().Format("Monday, January 2, 2006")
	b.WriteString(lipgloss.NewStyle().Bold(true).Render(today))
	b.WriteString("\n")

	switch p.mode {
	case journalModeView:
		b.WriteString(modeStyle.Render("Press ctrl+v for vim mode"))
	case journalModeVimNormal:
		indicator := "-- NORMAL --"
		if p.pendingKey != "" {
			indicator = fmt.Sprintf("-- NORMAL -- (%s...)", p.pendingKey)
		}
		b.WriteString(modeStyle.Render(indicator))
	case journalModeVimInsert:
		b.WriteString(modeStyle.Render("-- INSERT --"))
	}
	b.WriteString("\n\n")

	b.WriteString(p.textarea.View())

	if p.err != nil {
		b.WriteString("\n")
		b.WriteString(errorStyle.Render(fmt.Sprintf("Error: %v", p.err)))
	}

	b.WriteString("\n")
	if p.pendingSave {
		b.WriteString(statusStyle.Render("Saving..."))
	} else if p.textarea.Value() != p.lastSavedContent {
		b.WriteString(statusStyle.Render("Modified"))
	} else if p.lastSavedContent != "" {
		b.WriteString(statusStyle.Render("Saved"))
	}

	return b.String()
}

// Database commands

func loadOrCreateJournalEntryCmd(db *sql.DB) tea.Cmd {
	return func() tea.Msg {
		var id, content string
		err := db.QueryRow(`
			SELECT id, content FROM journal_entries
			WHERE entry_date = date('now', 'localtime')
		`).Scan(&id, &content)

		if err == sql.ErrNoRows {
			err = db.QueryRow(`
				INSERT INTO journal_entries (id, entry_date, content)
				VALUES (lower(hex(randomblob(16))), date('now', 'localtime'), '')
				RETURNING id
			`).Scan(&id)
			if err != nil {
				return journalEntryLoadFailedMsg{err: err}
			}
			return journalEntryLoadedMsg{id: id, content: ""}
		}

		if err != nil {
			return journalEntryLoadFailedMsg{err: err}
		}

		return journalEntryLoadedMsg{id: id, content: content}
	}
}

func saveJournalEntryCmd(db *sql.DB, entryID, content string) tea.Cmd {
	return func() tea.Msg {
		_, err := db.Exec(`
			UPDATE journal_entries
			SET content = ?, updated_at = CURRENT_TIMESTAMP
			WHERE id = ?
		`, content, entryID)

		if err != nil {
			return journalEntrySaveFailedMsg{err: err}
		}
		return journalEntrySavedMsg{}
	}
}

func startDebounceCmd(version int) tea.Cmd {
	return tea.Tick(journalDebounceInterval, func(t time.Time) tea.Msg {
		return journalDebounceTickMsg{version: version}
	})
}
