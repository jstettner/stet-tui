package main

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// HistoryPage displays historical task data (placeholder).
type HistoryPage struct {
	width  int
	height int
}

// NewHistoryPage creates and initializes the History page.
func NewHistoryPage() *HistoryPage {
	return &HistoryPage{}
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
}

func (p *HistoryPage) Update(msg tea.Msg) (Page, tea.Cmd) {
	// No interactive elements yet
	return p, nil
}

func (p *HistoryPage) View() string {
	return "History Contents (placeholder)"
}
