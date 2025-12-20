package main

import (
	"database/sql"
	"strings"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/paginator"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// pageInitializer is an optional interface for pages that need async initialization.
type pageInitializer interface {
	InitCmd() tea.Cmd
}

// globalKeyMap defines application-wide key bindings.
type globalKeyMap struct {
	Left  key.Binding
	Right key.Binding
	Help  key.Binding
	Quit  key.Binding
}

var globalKeys = globalKeyMap{
	Left: key.NewBinding(
		key.WithKeys("left"),
		key.WithHelp("←", "prev page"),
	),
	Right: key.NewBinding(
		key.WithKeys("right"),
		key.WithHelp("→", "next page"),
	),
	Help: key.NewBinding(
		key.WithKeys("?"),
		key.WithHelp("?", "help"),
	),
	Quit: key.NewBinding(
		key.WithKeys("q", "ctrl+c"),
		key.WithHelp("q", "quit"),
	),
}

// AppModel is the root Bubble Tea model that manages pages and global state.
type AppModel struct {
	pages       []Page
	paginator   paginator.Model
	help        help.Model
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
		help:        help.New(),
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
		Foreground(lipgloss.Color("#FFFFFF")).
		Render(t.text)
}

// combinedKeyMap implements help.KeyMap by combining page and global keys.
type combinedKeyMap struct {
	pageKeys []key.Binding
}

func (k combinedKeyMap) ShortHelp() []key.Binding {
	// Show page keys first, then global help and quit
	bindings := make([]key.Binding, 0, len(k.pageKeys)+2)
	bindings = append(bindings, k.pageKeys...)
	bindings = append(bindings, globalKeys.Help, globalKeys.Quit)
	return bindings
}

func (k combinedKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		k.pageKeys,
		{globalKeys.Left, globalKeys.Right, globalKeys.Help, globalKeys.Quit},
	}
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

// helpHeight returns the number of lines the help component will use.
func (m AppModel) helpHeight() int {
	if m.help.ShowAll {
		return 2 // Full help uses 2 rows (page keys + global keys)
	}
	return 1 // Short help uses 1 row
}

// contentHeight returns the available height for page content.
func (m AppModel) contentHeight() int {
	if m.height == 0 {
		return 0
	}
	// Layout: title(1) + \n\n(2) + content + \n\n(2) + help + \n\n(2) + paginator(1)
	// Plus docStyle vertical frame
	chrome := 1 + 2 + 2 + m.helpHeight() + 2 + 1 + docStyle.GetVerticalFrameSize()
	return max(m.height-chrome, 0)
}

// updatePageSizes notifies all pages of available dimensions.
func (m AppModel) updatePageSizes() {
	contentHeight := m.contentHeight()
	for _, page := range m.pages {
		page.SetSize(m.width, contentHeight)
	}
}

func (m AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.updatePageSizes()
		return m, nil

	case InvalidateTodayPageMsg:
		// Reset Today page's initialized state so it refetches on next view
		delete(m.initialized, TodayPageID)
		return m, nil

	case tea.KeyMsg:
		switch {
		case key.Matches(msg, globalKeys.Quit):
			return m, tea.Quit
		case key.Matches(msg, globalKeys.Help):
			m.help.ShowAll = !m.help.ShowAll
			m.updatePageSizes() // Recalculate since help height changed
			return m, nil
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

	// View help
	if m.width > 0 {
		contentWidth := max(m.width-docStyle.GetHorizontalFrameSize(), 0)
		if contentWidth > 0 {
			m.help.Width = contentWidth
		}
	}
	keyMap := combinedKeyMap{pageKeys: m.activePage().KeyMap()}
	b.WriteString(m.help.View(keyMap))
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
