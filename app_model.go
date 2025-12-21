package main

import (
	"database/sql"
	"strings"

	"stet.codes/tui/clients"
	"stet.codes/tui/pages"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/paginator"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Styles for dim page titles in the navigation indicator.
var (
	dimStyle1 = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#888888"))
	dimStyle2 = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#666666"))
)

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
	pages       []pages.Page
	paginator   paginator.Model
	help        help.Model
	initialized map[pages.PageID]bool
	width       int
	height      int
}

// NewAppModel creates and initializes the application model with all pages.
func NewAppModel(db *sql.DB, ouraClient *clients.OuraClient, plantaClient *clients.PlantaClient) AppModel {
	allPages := []pages.Page{
		pages.NewTodayPage(db),
		pages.NewOuraPage(ouraClient),
		pages.NewPlantaPage(plantaClient),
		pages.NewHistoryPage(db),
		pages.NewTaskCfgPage(db),
	}

	pag := paginator.New()
	pag.Type = paginator.Dots
	pag.ActiveDot = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "235", Dark: "252"}).Render("•")
	pag.InactiveDot = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "250", Dark: "238"}).Render("•")
	pag.SetTotalPages(len(allPages))

	return AppModel{
		pages:       allPages,
		paginator:   pag,
		help:        help.New(),
		initialized: make(map[pages.PageID]bool),
	}
}

// activePage returns the currently active page.
func (m AppModel) activePage() pages.Page {
	idx := m.paginator.Page
	if idx < 0 || idx >= len(m.pages) {
		panic("invalid page index")
	}
	return m.pages[idx]
}

// visiblePage represents a page to display in the navigation indicator.
type visiblePage struct {
	index    int
	dimLevel int // 0 = full color, 1 = dim, 2 = dimmer
}

// visiblePagesResult contains the visible pages and whether there are more pages in each direction.
type visiblePagesResult struct {
	pages    []visiblePage
	hasLeft  bool
	hasRight bool
}

// getVisiblePages returns up to 3 pages to display with their dim levels,
// plus indicators for whether more pages exist in each direction.
func getVisiblePages(current, total int) visiblePagesResult {
	if total < 3 {
		// Fewer than 3 pages - show all with appropriate dimming
		pages := make([]visiblePage, total)
		for i := 0; i < total; i++ {
			dim := current - i
			if dim < 0 {
				dim = -dim
			}
			pages[i] = visiblePage{index: i, dimLevel: dim}
		}
		return visiblePagesResult{pages: pages, hasLeft: false, hasRight: false}
	}

	// Determine the window of 3 pages to show
	start := current - 1
	if start < 0 {
		start = 0
	}
	if start+3 > total {
		start = total - 3
	}

	pages := make([]visiblePage, 3)
	for i := 0; i < 3; i++ {
		idx := start + i
		dim := current - idx
		if dim < 0 {
			dim = -dim
		}
		pages[i] = visiblePage{index: idx, dimLevel: dim}
	}
	return visiblePagesResult{
		pages:    pages,
		hasLeft:  start > 0,
		hasRight: start+3 < total,
	}
}

// renderTitle renders the navigation indicator showing current and adjacent pages.
func (m AppModel) renderTitle() string {
	result := getVisiblePages(m.paginator.Page, len(m.pages))
	titles := make([]string, len(result.pages))

	for i, vp := range result.pages {
		t := m.pages[vp.index].Title()
		var styled string
		switch vp.dimLevel {
		case 0:
			// Current page: full color
			styled = lipgloss.NewStyle().
				Background(t.Color).
				Foreground(lipgloss.Color("#FFFFFF")).
				Render(t.Text)
		case 1:
			styled = dimStyle1.Render(t.Text)
		default:
			styled = dimStyle2.Render(t.Text)
		}
		titles[i] = styled
	}

	// Build the title bar with consistent spacing for arrows
	var b strings.Builder

	// Left arrow slot (always same width for consistent spacing)
	if result.hasLeft {
		b.WriteString("←")
	} else {
		b.WriteString(" ")
	}
	b.WriteString("   ")

	// Page titles
	b.WriteString(strings.Join(titles, "   "))

	// Right arrow slot (always same width for consistent spacing)
	b.WriteString("   ")
	if result.hasRight {
		b.WriteString("→")
	} else {
		b.WriteString(" ")
	}

	return b.String()
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
	// Initialize the active page if it implements PageInitializer
	page := m.activePage()
	if pi, ok := page.(pages.PageInitializer); ok {
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
	// Plus DocStyle vertical frame
	chrome := 1 + 2 + 2 + m.helpHeight() + 2 + 1 + pages.DocStyle.GetVerticalFrameSize()
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

	case pages.InvalidateTodayPageMsg:
		// Reset Today page's initialized state so it refetches on next view
		delete(m.initialized, pages.TodayPageID)
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
	if nc, ok := m.activePage().(pages.NavigationCapturer); ok {
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
		if pi, ok := page.(pages.PageInitializer); ok && !m.initialized[page.ID()] {
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
		contentWidth := max(m.width-pages.DocStyle.GetHorizontalFrameSize(), 0)
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
		contentWidth := max(m.width-pages.DocStyle.GetHorizontalFrameSize(), 0)
		if contentWidth > 0 {
			paginatorView = lipgloss.PlaceHorizontal(contentWidth, lipgloss.Center, paginatorView)
		}
	}
	b.WriteString(paginatorView)

	// Size the outer container to exactly match the terminal window.
	// This ensures we always render a full-height screen (no 20-row cap).
	s := pages.DocStyle
	if m.width > 0 {
		s = s.Width(m.width)
	}
	if m.height > 0 {
		s = s.Height(m.height)
	}
	return s.Render(b.String())
}
