package main

import (
	"database/sql"
	"strings"

	"github.com/charmbracelet/bubbles/paginator"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// pageInitializer is an optional interface for pages that need async initialization.
type pageInitializer interface {
	InitCmd() tea.Cmd
}

// AppModel is the root Bubble Tea model that manages pages and global state.
type AppModel struct {
	pages       []Page
	paginator   paginator.Model
	initialized map[PageID]bool
	width       int
	height      int
}

// NewAppModel creates and initializes the application model with all pages.
func NewAppModel(db *sql.DB) AppModel {
	pages := []Page{
		NewTodayPage(db),
		NewHistoryPage(db),
		NewTaskCfgPage(db),
	}

	p := paginator.New()
	p.Type = paginator.Dots
	p.ActiveDot = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "235", Dark: "252"}).Render("•")
	p.InactiveDot = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "250", Dark: "238"}).Render("•")
	p.SetTotalPages(len(pages))

	return AppModel{
		pages:       pages,
		paginator:   p,
		initialized: make(map[PageID]bool),
	}
}

// activePage returns the currently active page.
func (m AppModel) activePage() Page {
	idx := m.paginator.Page
	if idx < 0 || idx >= len(m.pages) {
		panic("invalid page index")
	}
	return m.pages[idx]
}

// renderTitle renders the header title for the active page.
func (m AppModel) renderTitle() string {
	t := m.activePage().Title()
	return lipgloss.NewStyle().
		Background(t.color).
		Render(t.text)
}

func (m AppModel) Init() tea.Cmd {
	// Initialize the active page if it implements pageInitializer
	page := m.activePage()
	if pi, ok := page.(pageInitializer); ok {
		m.initialized[page.ID()] = true
		return pi.InitCmd()
	}
	return nil
}

func (m AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// Notify all pages of the new size
		for _, page := range m.pages {
			page.SetSize(m.width, m.height)
		}
		return m, nil

	case InvalidateTodayPageMsg:
		// Reset Today page's initialized state so it refetches on next view
		delete(m.initialized, TodayPageID)
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		}
	}

	// Track previous page to detect navigation
	prevPage := m.paginator.Page

	// Check if active page captures navigation keys (e.g., text input mode)
	capturesNav := false
	if nc, ok := m.activePage().(navigationCapturer); ok {
		capturesNav = nc.CapturesNavigation()
	}

	// Update paginator for navigation (left/right keys) unless page captures them
	var paginatorCmd tea.Cmd
	if !capturesNav {
		m.paginator, paginatorCmd = m.paginator.Update(msg)
	}

	// Update only the active page
	idx := m.paginator.Page
	var pageCmd tea.Cmd
	m.pages[idx], pageCmd = m.pages[idx].Update(msg)

	var cmds []tea.Cmd
	if paginatorCmd != nil {
		cmds = append(cmds, paginatorCmd)
	}
	if pageCmd != nil {
		cmds = append(cmds, pageCmd)
	}

	// If page changed, initialize the new page if it hasn't been initialized yet
	if idx != prevPage {
		page := m.pages[idx]
		if pi, ok := page.(pageInitializer); ok && !m.initialized[page.ID()] {
			m.initialized[page.ID()] = true
			cmds = append(cmds, pi.InitCmd())
		}
	}

	return m, tea.Batch(cmds...)
}

func (m AppModel) View() string {
	var b strings.Builder

	// View title
	b.WriteString(m.renderTitle())
	b.WriteString("\n\n")

	// View contents from active page
	b.WriteString(m.activePage().View())
	b.WriteString("\n\n")

	// View tab indicator (paginator)
	paginatorView := m.paginator.View()
	if m.width > 0 {
		contentWidth := max(m.width-docStyle.GetHorizontalFrameSize(), 0)
		if contentWidth > 0 {
			paginatorView = lipgloss.PlaceHorizontal(contentWidth, lipgloss.Center, paginatorView)
		}
	}
	b.WriteString(paginatorView)

	// Size the outer container to exactly match the terminal window.
	// This ensures we always render a full-height screen (no 20-row cap).
	s := docStyle
	if m.width > 0 {
		s = s.Width(m.width)
	}
	if m.height > 0 {
		s = s.Height(m.height)
	}
	return s.Render(b.String())
}
