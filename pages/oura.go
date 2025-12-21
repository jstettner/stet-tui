package pages

import (
	"context"
	"fmt"
	"strings"
	"time"

	"stet.codes/tui/clients"

	"github.com/NimbleMarkets/ntcharts/linechart/timeserieslinechart"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const ouraPollInterval = 20 * time.Second

// Oura page message types
type ouraTickMsg time.Time

type OuraDataLoadedMsg struct {
	readiness *clients.DailyReadiness
	heartRate []clients.HeartRatePoint
}

type OuraDataFailedMsg struct {
	err error
}

type ouraAuthCompleteMsg struct {
	tokens *clients.OuraTokens
}

type ouraAuthFailedMsg struct {
	err error
}

// ouraKeyMap defines key bindings for the Oura page.
type ouraKeyMap struct {
	Auth    key.Binding
	Refresh key.Binding
}

var ouraKeys = ouraKeyMap{
	Auth: key.NewBinding(
		key.WithKeys("a"),
		key.WithHelp("a", "authenticate"),
	),
	Refresh: key.NewBinding(
		key.WithKeys("r"),
		key.WithHelp("r", "refresh"),
	),
}

// hrHighlightStyle is the style for the vertical line on the chart at the selected time
var hrHighlightStyle = lipgloss.NewStyle().Background(lipgloss.Color("#444444"))

// OuraPage displays Oura health data.
type OuraPage struct {
	client       *clients.OuraClient
	readiness    *clients.DailyReadiness
	heartRate    []clients.HeartRatePoint
	hrChart      timeserieslinechart.Model
	hrTable      table.Model
	selectedTime time.Time // timestamp of the currently selected heart rate point
	pollCount    int
	lastPoll     time.Time
	err          error
	loading      bool
	needsAuth    bool
	authPending  bool
	authCancel   context.CancelFunc
	width        int
	height       int
}

// NewOuraPage creates and initializes the Oura page.
func NewOuraPage(client *clients.OuraClient) *OuraPage {
	needsAuth := !client.Auth().HasCredentials() || !client.IsAuthenticated()
	return &OuraPage{
		client:    client,
		needsAuth: needsAuth,
		loading:   !needsAuth,
	}
}

func (p *OuraPage) ID() PageID {
	return OuraPageID
}

func (p *OuraPage) Title() Title {
	return Title{
		Text:  "Oura",
		Color: lipgloss.Color("#8B5CF6"), // Purple for Oura
	}
}

func (p *OuraPage) SetSize(width, height int) {
	p.width = width
	p.height = height
	// Rebuild chart and table with new dimensions
	if len(p.heartRate) > 0 {
		p.buildHeartRateChart()
		p.buildHeartRateTable()
		p.updateChartHighlight()
	}
}

// InitCmd returns the initial command to start polling.
func (p *OuraPage) InitCmd() tea.Cmd {
	// Recheck auth state at initialization time, not creation time
	// This avoids a race condition where tokens may still be loading when the page is created
	p.needsAuth = !p.client.Auth().HasCredentials() || !p.client.IsAuthenticated()
	p.loading = !p.needsAuth

	if p.needsAuth {
		return nil // Don't start polling if auth is needed
	}
	return tea.Batch(
		p.fetchDataCmd(),
		ouraTickCmd(),
	)
}

// ouraTickCmd returns a command that sends a tick message after the poll interval.
func ouraTickCmd() tea.Cmd {
	return tea.Tick(ouraPollInterval, func(t time.Time) tea.Msg {
		return ouraTickMsg(t)
	})
}

// fetchDataCmd returns a command that fetches readiness and heart rate data.
func (p *OuraPage) fetchDataCmd() tea.Cmd {
	return func() tea.Msg {
		readiness, err := p.client.GetTodayReadiness()
		if err != nil {
			return OuraDataFailedMsg{err: err}
		}

		heartRate, err := p.client.GetTodayHeartRate()
		if err != nil {
			// Don't fail completely if heart rate fails, just log it
			heartRate = nil
		}

		return OuraDataLoadedMsg{readiness: readiness, heartRate: heartRate}
	}
}

// startAuthCmd starts the OAuth2 flow.
func (p *OuraPage) startAuthCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		p.authCancel = cancel

		tokensChan, errChan := p.client.Auth().StartAuthFlow(ctx)

		select {
		case tokens := <-tokensChan:
			if tokens != nil {
				return ouraAuthCompleteMsg{tokens: tokens}
			}
		case err := <-errChan:
			if err != nil {
				return ouraAuthFailedMsg{err: err}
			}
		}

		return ouraAuthFailedMsg{err: fmt.Errorf("authentication cancelled")}
	}
}

func (p *OuraPage) Update(msg tea.Msg) (Page, tea.Cmd) {
	switch msg := msg.(type) {
	case ouraTickMsg:
		if p.needsAuth || p.authPending {
			return p, ouraTickCmd() // Keep ticking but don't fetch
		}
		p.pollCount++
		p.loading = true
		return p, tea.Batch(p.fetchDataCmd(), ouraTickCmd())

	case OuraDataLoadedMsg:
		p.readiness = msg.readiness
		p.heartRate = msg.heartRate
		p.lastPoll = time.Now()
		p.loading = false
		p.err = nil

		// Build the heart rate chart and table if we have data
		if len(p.heartRate) > 0 {
			p.buildHeartRateChart()
			p.buildHeartRateTable()
			// Initialize highlight at the first row (most recent data point)
			p.updateChartHighlight()
		}
		return p, nil

	case OuraDataFailedMsg:
		p.err = msg.err
		p.loading = false
		// Check if it's an auth error
		if strings.Contains(msg.err.Error(), "not authenticated") {
			p.needsAuth = true
		}
		return p, nil

	case ouraAuthCompleteMsg:
		p.authPending = false
		p.needsAuth = false
		p.loading = true
		p.err = nil
		// Start fetching data now that we're authenticated
		return p, tea.Batch(p.fetchDataCmd(), ouraTickCmd())

	case ouraAuthFailedMsg:
		p.authPending = false
		p.err = msg.err
		return p, nil

	case tea.KeyMsg:
		switch {
		case key.Matches(msg, ouraKeys.Auth):
			if !p.client.Auth().HasCredentials() {
				p.err = fmt.Errorf("missing OURA_CLIENT_ID and OURA_CLIENT_SECRET in .env")
				return p, nil
			}
			if p.authPending {
				return p, nil // Already authenticating
			}
			p.authPending = true
			p.err = nil
			return p, p.startAuthCmd()

		case key.Matches(msg, ouraKeys.Refresh):
			if p.needsAuth || p.authPending {
				return p, nil
			}
			p.loading = true
			return p, p.fetchDataCmd()
		}

		// Forward key events to the table for navigation
		if len(p.heartRate) > 0 {
			var cmd tea.Cmd
			p.hrTable, cmd = p.hrTable.Update(msg)
			// Update the chart highlight to match the selected row
			p.updateChartHighlight()
			return p, cmd
		}
	}

	return p, nil
}

// buildHeartRateChart creates the heart rate chart from the data.
func (p *OuraPage) buildHeartRateChart() {
	chartWidth := max(p.width-DocStyle.GetHorizontalFrameSize()-4, 40)
	chartHeight := 8

	p.hrChart = timeserieslinechart.New(chartWidth, chartHeight)

	// Add heart rate points to chart
	for _, hr := range p.heartRate {
		// Parse timestamp (ISO 8601 format)
		t, err := time.Parse(time.RFC3339, hr.Timestamp)
		if err != nil {
			continue
		}
		p.hrChart.Push(timeserieslinechart.TimePoint{Time: t, Value: float64(hr.BPM)})
	}

	// Draw the chart using braille characters for higher resolution
	p.hrChart.DrawBraille()
}

// buildHeartRateTable creates the heart rate table from the data.
func (p *OuraPage) buildHeartRateTable() {
	columns := []table.Column{
		{Title: "Time", Width: 10},
		{Title: "BPM", Width: 6},
		{Title: "Source", Width: 10},
	}

	// Build rows in reverse order (most recent first)
	rows := make([]table.Row, 0, len(p.heartRate))
	for i := len(p.heartRate) - 1; i >= 0; i-- {
		hr := p.heartRate[i]
		// Parse timestamp and format as HH:MM:SS in local time
		t, err := time.Parse(time.RFC3339, hr.Timestamp)
		timeStr := hr.Timestamp
		if err == nil {
			timeStr = t.Local().Format("15:04:05")
		}
		rows = append(rows, table.Row{timeStr, fmt.Sprintf("%d", hr.BPM), hr.Source})
	}

	// Create table with purple accent styling
	s := table.DefaultStyles()
	s.Header = s.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("#8B5CF6")).
		BorderBottom(true).
		Bold(true)
	s.Selected = s.Selected.
		Foreground(lipgloss.Color("#FFFFFF")).
		Background(lipgloss.Color("#8B5CF6")).
		Bold(false)

	// Calculate available height for the table
	// Account for: title(2) + score(2) + contributors header+grid(5) +
	// hr chart section(11) + "Recent Samples" header(1) + status(2) + padding
	fixedContentHeight := 23 + DocStyle.GetVerticalFrameSize()
	tableHeight := max(p.height-fixedContentHeight, 5) // minimum 5 rows

	p.hrTable = table.New(
		table.WithColumns(columns),
		table.WithRows(rows),
		table.WithFocused(true),
		table.WithHeight(tableHeight),
		table.WithStyles(s),
	)
}

// updateChartHighlight updates the chart to show a vertical line at the selected time
func (p *OuraPage) updateChartHighlight() {
	if len(p.heartRate) == 0 {
		return
	}

	// Get the selected row index from the table cursor
	cursor := p.hrTable.Cursor()

	// Table rows are in reverse order (most recent first)
	// so cursor 0 = heartRate[len-1], cursor 1 = heartRate[len-2], etc.
	hrIndex := len(p.heartRate) - 1 - cursor
	if hrIndex < 0 || hrIndex >= len(p.heartRate) {
		return
	}

	// Parse the timestamp of the selected point
	t, err := time.Parse(time.RFC3339, p.heartRate[hrIndex].Timestamp)
	if err != nil {
		return
	}

	p.selectedTime = t

	// Rebuild the chart to clear previous highlight, then apply new one
	p.buildHeartRateChart()
	p.hrChart.SetColumnBackgroundStyle(t, hrHighlightStyle)
}

func (p *OuraPage) View() string {
	var b strings.Builder

	contentWidth := max(p.width-DocStyle.GetHorizontalFrameSize(), 40)

	// Title style
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#8B5CF6")).
		MarginBottom(1)

	// Score style
	scoreStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#FFFFFF")).
		Background(lipgloss.Color("#8B5CF6")).
		Padding(0, 2)

	// Info style
	infoStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#888888"))

	// Error style
	errorStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FF6B6B"))

	// Check for missing credentials first
	if !p.client.Auth().HasCredentials() {
		b.WriteString(titleStyle.Render("Oura Ring"))
		b.WriteString("\n\n")
		b.WriteString(errorStyle.Render("Missing OAuth2 credentials"))
		b.WriteString("\n\n")
		b.WriteString("1. Create an app at https://cloud.ouraring.com/oauth/applications\n")
		b.WriteString("2. Set redirect URI to: http://localhost:8089/callback\n")
		b.WriteString("3. Copy credentials to your .env file:\n")
		b.WriteString("   OURA_CLIENT_ID=your_client_id\n")
		b.WriteString("   OURA_CLIENT_SECRET=your_client_secret\n")
		b.WriteString("4. Restart the app\n")
		return b.String()
	}

	// Auth pending state
	if p.authPending {
		b.WriteString(titleStyle.Render("Oura Ring"))
		b.WriteString("\n\n")
		b.WriteString("Opening browser for authentication...\n")
		b.WriteString("Please authorize the app in your browser.\n")
		return b.String()
	}

	// Need auth state
	if p.needsAuth {
		b.WriteString(titleStyle.Render("Oura Ring"))
		b.WriteString("\n\n")
		b.WriteString("Authentication required.\n\n")
		b.WriteString("Press 'a' to authenticate with Oura.\n")
		if p.err != nil {
			b.WriteString("\n")
			b.WriteString(errorStyle.Render(fmt.Sprintf("Error: %v", p.err)))
		}
		return b.String()
	}

	// Normal display
	b.WriteString(titleStyle.Render("Oura Ring - Daily Readiness"))
	b.WriteString("\n\n")

	if p.loading && p.readiness == nil {
		b.WriteString("Loading...\n")
	} else if p.readiness != nil {
		// Display score prominently
		scoreLabel := fmt.Sprintf(" Readiness Score: %d ", p.readiness.Score)
		b.WriteString(scoreStyle.Render(scoreLabel))
		b.WriteString("\n\n")

		// Display contributors in a grid (these are contribution scores 0-100, not raw values)
		b.WriteString(infoStyle.Render("Contribution Scores:"))
		b.WriteString("\n")
		contributorStyle := lipgloss.NewStyle().Width(contentWidth / 2)

		contributors := []struct {
			name  string
			value int
		}{
			{"Activity Balance", p.readiness.Contributors.ActivityBalance},
			{"Body Temp", p.readiness.Contributors.BodyTemperature},
			{"HRV Balance", p.readiness.Contributors.HRVBalance},
			{"Prev Day Activity", p.readiness.Contributors.PreviousDayActivity},
			{"Previous Night", p.readiness.Contributors.PreviousNight},
			{"Recovery Index", p.readiness.Contributors.RecoveryIndex},
			{"Resting HR", p.readiness.Contributors.RestingHeartRate},
			{"Sleep Balance", p.readiness.Contributors.SleepBalance},
		}

		for i, c := range contributors {
			line := fmt.Sprintf("%-22s %3d", c.name, c.value)
			if i%2 == 0 {
				b.WriteString(contributorStyle.Render(line))
			} else {
				b.WriteString(line)
				b.WriteString("\n")
			}
		}
		b.WriteString("\n")

		// Display heart rate chart
		if len(p.heartRate) > 0 {
			b.WriteString(infoStyle.Render("Heart Rate (BPM):"))
			b.WriteString("\n")
			b.WriteString(p.hrChart.View())
			b.WriteString("\n")

			// Show min/max/avg heart rate
			var minHR, maxHR, sumHR int
			minHR = 999
			for _, hr := range p.heartRate {
				if hr.BPM < minHR {
					minHR = hr.BPM
				}
				if hr.BPM > maxHR {
					maxHR = hr.BPM
				}
				sumHR += hr.BPM
			}
			avgHR := sumHR / len(p.heartRate)
			b.WriteString(infoStyle.Render(fmt.Sprintf("Min: %d  Avg: %d  Max: %d  (%d readings)", minHR, avgHR, maxHR, len(p.heartRate))))
			b.WriteString("\n\n")

			// Display heart rate table
			b.WriteString(infoStyle.Render("Recent Samples:"))
			b.WriteString("\n")
			b.WriteString(p.hrTable.View())
			b.WriteString("\n")
		}
	} else if p.err == nil {
		b.WriteString("No readiness data available for today yet.\n")
	}

	// Error display
	if p.err != nil && !p.loading {
		b.WriteString("\n")
		b.WriteString(errorStyle.Render(fmt.Sprintf("Error: %v", p.err)))
		b.WriteString("\n")
	}

	// Status line
	b.WriteString("\n")
	statusParts := []string{}
	statusParts = append(statusParts, fmt.Sprintf("Poll count: %d", p.pollCount))
	if !p.lastPoll.IsZero() {
		statusParts = append(statusParts, fmt.Sprintf("Last updated: %s", p.lastPoll.Format("15:04:05")))
	}
	if p.loading {
		statusParts = append(statusParts, "Refreshing...")
	}
	b.WriteString(infoStyle.Render(strings.Join(statusParts, " | ")))

	return b.String()
}

func (p *OuraPage) KeyMap() []key.Binding {
	if p.needsAuth && p.client.Auth().HasCredentials() {
		return []key.Binding{ouraKeys.Auth}
	}
	if !p.needsAuth && !p.authPending {
		return []key.Binding{ouraKeys.Refresh}
	}
	return []key.Binding{}
}
