package pages

import (
	"fmt"
	"strings"
	"time"

	"stet.codes/tui/clients"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const plantaPollInterval = 4 * time.Hour

// Planta page message types
type plantaTickMsg time.Time

type plantaDataLoadedMsg struct {
	tasks []clients.PlantTask
}

type plantaDataFailedMsg struct {
	err error
}

type plantaCompleteSuccessMsg struct {
	plantID    string
	actionType clients.ActionType
}

type plantaCompleteFailedMsg struct {
	err error
}

// plantaKeyMap defines key bindings for the Planta page.
type plantaKeyMap struct {
	Up       key.Binding
	Down     key.Binding
	Complete key.Binding
	Refresh  key.Binding
}

var plantaKeys = plantaKeyMap{
	Up: key.NewBinding(
		key.WithKeys("up", "k"),
		key.WithHelp("k/up", "move up"),
	),
	Down: key.NewBinding(
		key.WithKeys("down", "j"),
		key.WithHelp("j/down", "move down"),
	),
	Complete: key.NewBinding(
		key.WithKeys("enter", "c"),
		key.WithHelp("enter/c", "complete"),
	),
	Refresh: key.NewBinding(
		key.WithKeys("r"),
		key.WithHelp("r", "refresh"),
	),
}

// PlantaPage displays plant care tasks from Planta.
type PlantaPage struct {
	client     *clients.PlantaClient
	tasks      []clients.PlantTask
	cursor     int
	pollCount  int
	lastPoll   time.Time
	err        error
	loading    bool
	completing bool
	needsAuth  bool
	width      int
	height     int
}

// NewPlantaPage creates and initializes the Planta page.
func NewPlantaPage(client *clients.PlantaClient) *PlantaPage {
	needsAuth := !client.Auth().HasCredentials()
	return &PlantaPage{
		client:    client,
		needsAuth: needsAuth,
		loading:   !needsAuth,
	}
}

func (p *PlantaPage) ID() PageID {
	return PlantaPageID
}

func (p *PlantaPage) Title() Title {
	return Title{
		Text:  "Planta",
		Color: lipgloss.Color("#22C55E"), // Green for plants
	}
}

func (p *PlantaPage) SetSize(width, height int) {
	p.width = width
	p.height = height
}

// InitCmd returns the initial command to start polling.
func (p *PlantaPage) InitCmd() tea.Cmd {
	// Recheck auth state at initialization time for consistency
	p.needsAuth = !p.client.Auth().HasCredentials()
	p.loading = !p.needsAuth

	if p.needsAuth {
		return nil
	}
	return tea.Batch(
		p.fetchDataCmd(),
		plantaTickCmd(),
	)
}

// plantaTickCmd returns a command that sends a tick message after the poll interval.
func plantaTickCmd() tea.Cmd {
	return tea.Tick(plantaPollInterval, func(t time.Time) tea.Msg {
		return plantaTickMsg(t)
	})
}

// fetchDataCmd returns a command that fetches plant tasks.
func (p *PlantaPage) fetchDataCmd() tea.Cmd {
	return func() tea.Msg {
		// Ensure authenticated (exchanges code if needed)
		if err := p.client.EnsureAuthenticated(); err != nil {
			return plantaDataFailedMsg{err: err}
		}

		tasks, err := p.client.GetDueTasks(3) // Today + next 3 days
		if err != nil {
			return plantaDataFailedMsg{err: err}
		}

		return plantaDataLoadedMsg{tasks: tasks}
	}
}

// completeTaskCmd returns a command that completes a task.
func (p *PlantaPage) completeTaskCmd(task clients.PlantTask) tea.Cmd {
	return func() tea.Msg {
		err := p.client.CompleteAction(task.PlantID, task.ActionType)
		if err != nil {
			return plantaCompleteFailedMsg{err: err}
		}
		return plantaCompleteSuccessMsg{
			plantID:    task.PlantID,
			actionType: task.ActionType,
		}
	}
}

func (p *PlantaPage) Update(msg tea.Msg) (Page, tea.Cmd) {
	switch msg := msg.(type) {
	case plantaTickMsg:
		if p.needsAuth || p.completing {
			return p, plantaTickCmd()
		}
		p.pollCount++
		p.loading = true
		return p, tea.Batch(p.fetchDataCmd(), plantaTickCmd())

	case plantaDataLoadedMsg:
		p.tasks = msg.tasks
		p.lastPoll = time.Now()
		p.loading = false
		p.err = nil
		// Clamp cursor to valid range
		if p.cursor >= len(p.tasks) {
			p.cursor = max(len(p.tasks)-1, 0)
		}
		return p, nil

	case plantaDataFailedMsg:
		p.err = msg.err
		p.loading = false
		if strings.Contains(msg.err.Error(), "missing PLANTA_APP_CODE") {
			p.needsAuth = true
		}
		return p, nil

	case plantaCompleteSuccessMsg:
		p.completing = false
		// Remove the completed task from list
		for i, t := range p.tasks {
			if t.PlantID == msg.plantID && t.ActionType == msg.actionType {
				p.tasks = append(p.tasks[:i], p.tasks[i+1:]...)
				break
			}
		}
		// Clamp cursor
		if p.cursor >= len(p.tasks) {
			p.cursor = max(len(p.tasks)-1, 0)
		}
		return p, nil

	case plantaCompleteFailedMsg:
		p.completing = false
		p.err = msg.err
		return p, nil

	case tea.KeyMsg:
		switch {
		case key.Matches(msg, plantaKeys.Up):
			if p.cursor > 0 {
				p.cursor--
			}
			return p, nil

		case key.Matches(msg, plantaKeys.Down):
			if p.cursor < len(p.tasks)-1 {
				p.cursor++
			}
			return p, nil

		case key.Matches(msg, plantaKeys.Complete):
			if len(p.tasks) == 0 || p.completing || p.needsAuth {
				return p, nil
			}
			task := p.tasks[p.cursor]
			if !task.Completable {
				p.err = fmt.Errorf("%s cannot be completed via API", task.ActionType)
				return p, nil
			}
			p.completing = true
			p.err = nil
			return p, p.completeTaskCmd(task)

		case key.Matches(msg, plantaKeys.Refresh):
			if p.needsAuth || p.completing {
				return p, nil
			}
			p.loading = true
			return p, p.fetchDataCmd()
		}
	}

	return p, nil
}

func (p *PlantaPage) View() string {
	var b strings.Builder

	// Styles
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#22C55E")).
		MarginBottom(1)

	errorStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FF6B6B"))

	infoStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#888888"))

	overdueStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FF6B6B"))

	todayStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FBBF24"))

	upcomingStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#22C55E"))

	selectedBg := lipgloss.NewStyle().
		Background(lipgloss.Color("#333333"))

	manualStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#666666"))

	// Check for missing credentials
	if p.needsAuth {
		b.WriteString(titleStyle.Render("Planta - Plant Care"))
		b.WriteString("\n\n")
		b.WriteString(errorStyle.Render("Missing PLANTA_APP_CODE"))
		b.WriteString("\n\n")
		b.WriteString("1. Get your Planta app code\n")
		b.WriteString("2. Add to your .env file:\n")
		b.WriteString("   PLANTA_APP_CODE=your_planta_app_code\n")
		b.WriteString("3. Restart the app\n")
		return lipgloss.NewStyle().Height(p.height).Render(b.String())
	}

	// Title
	b.WriteString(titleStyle.Render("Planta - Plant Care Tasks"))
	b.WriteString("\n\n")

	// Loading state
	if p.loading && len(p.tasks) == 0 {
		b.WriteString("Loading...\n")
		return lipgloss.NewStyle().Height(p.height).Render(b.String())
	}

	// No tasks
	if len(p.tasks) == 0 {
		b.WriteString(infoStyle.Render("No tasks due in the next 3 days."))
		b.WriteString("\n")
	} else {
		// Render task list
		for i, task := range p.tasks {
			// Icon for action type
			var icon string
			switch task.ActionType {
			case clients.ActionWatering:
				icon = "W"
			case clients.ActionFertilizing:
				icon = "F"
			case clients.ActionMisting:
				icon = "M"
			case clients.ActionCleaning:
				icon = "C"
			case clients.ActionRepotting:
				icon = "R"
			case clients.ActionProgressUpdate:
				icon = "P"
			}

			// Date display
			dateStr := task.DueDate.Format("Mon Jan 2")
			if task.IsToday {
				dateStr = "Today"
			} else if task.IsOverdue {
				dateStr = task.DueDate.Format("Jan 2") + " (overdue)"
			}

			// Truncate plant name if too long
			plantName := task.PlantName
			if len(plantName) > 15 {
				plantName = plantName[:12] + "..."
			}

			// Build line
			line := fmt.Sprintf("[%s] %-15s %-14s %s",
				icon,
				plantName,
				task.ActionType,
				dateStr,
			)

			// Apply styling based on urgency
			var styled string
			if task.IsOverdue {
				styled = overdueStyle.Render(line)
			} else if task.IsToday {
				styled = todayStyle.Render(line)
			} else {
				styled = upcomingStyle.Render(line)
			}

			// Add manual indicator for non-completable
			if !task.Completable {
				styled += manualStyle.Render(" [manual]")
			}

			// Highlight selected
			if i == p.cursor {
				styled = selectedBg.Render("> " + styled)
			} else {
				styled = "  " + styled
			}

			b.WriteString(styled)
			b.WriteString("\n")
		}
	}

	// Error display
	if p.err != nil && !p.loading {
		b.WriteString("\n")
		b.WriteString(errorStyle.Render(fmt.Sprintf("Error: %v", p.err)))
		b.WriteString("\n")
	}

	// Completing indicator
	if p.completing {
		b.WriteString("\n")
		b.WriteString(infoStyle.Render("Completing task..."))
		b.WriteString("\n")
	}

	// Status line
	b.WriteString("\n")
	statusParts := []string{}
	statusParts = append(statusParts, fmt.Sprintf("Tasks: %d", len(p.tasks)))
	if !p.lastPoll.IsZero() {
		statusParts = append(statusParts, fmt.Sprintf("Updated: %s", p.lastPoll.Format("15:04:05")))
	}
	if p.loading {
		statusParts = append(statusParts, "Refreshing...")
	}
	b.WriteString(infoStyle.Render(strings.Join(statusParts, " | ")))

	// Fill the available height so help/commands appear at the bottom
	return lipgloss.NewStyle().Height(p.height).Render(b.String())
}

func (p *PlantaPage) KeyMap() []key.Binding {
	if p.needsAuth {
		return []key.Binding{}
	}
	return []key.Binding{
		plantaKeys.Up,
		plantaKeys.Down,
		plantaKeys.Complete,
		plantaKeys.Refresh,
	}
}
