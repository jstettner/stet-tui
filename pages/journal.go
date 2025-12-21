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
	journalModeView journalMode = iota
	journalModeEdit
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
	Edit   key.Binding
	Escape key.Binding
}

var journalKeys = journalKeyMap{
	Edit: key.NewBinding(
		key.WithKeys("i"),
		key.WithHelp("i", "edit"),
	),
	Escape: key.NewBinding(
		key.WithKeys("esc"),
		key.WithHelp("esc", "view"),
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
	return p.mode == journalModeEdit
}

func (p *JournalPage) KeyMap() []key.Binding {
	if p.mode == journalModeEdit {
		return []key.Binding{journalKeys.Escape}
	}
	return []key.Binding{journalKeys.Edit}
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

	if p.mode == journalModeEdit {
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
		if msg.String() == "i" {
			p.mode = journalModeEdit
			p.textarea.Focus()
			return p, textarea.Blink
		}

	case journalModeEdit:
		if msg.String() == "esc" {
			p.mode = journalModeView
			p.textarea.Blur()

			if p.textarea.Value() != p.lastSavedContent {
				p.pendingSave = true
				return p, saveJournalEntryCmd(p.db, p.entryID, p.textarea.Value())
			}
			return p, nil
		}

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

func (p *JournalPage) View() string {
	var b strings.Builder

	modeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#888888"))
	errorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#FF6B6B"))
	statusStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))

	today := time.Now().Format("Monday, January 2, 2006")
	b.WriteString(lipgloss.NewStyle().Bold(true).Render(today))
	b.WriteString("\n")

	if p.mode == journalModeEdit {
		b.WriteString(modeStyle.Render("-- INSERT --"))
	} else {
		b.WriteString(modeStyle.Render("Press 'i' to edit"))
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
