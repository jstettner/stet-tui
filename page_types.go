package main

import (
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// PageID identifies each page/view in the application.
type PageID int

const (
	TodayPageID PageID = iota
	HistoryPageID
	TaskCfgPageID
	pageCount
)

// title holds the display text and color for a page's header.
type title struct {
	text  string
	color lipgloss.Color
}

// navigationCapturer is an optional interface for pages that need to capture
// navigation keys (left/right arrows) in certain modes (e.g., text input).
type navigationCapturer interface {
	CapturesNavigation() bool
}

// Page is the interface that all pages must implement.
// Each page manages its own state, handles updates, and renders its content.
type Page interface {
	// ID returns the unique identifier for this page.
	ID() PageID

	// Title returns the page's header title configuration.
	Title() title

	// SetSize is called when the window resizes so the page can adjust its layout.
	SetSize(width, height int)

	// Update handles messages and returns the updated page and any command.
	Update(msg tea.Msg) (Page, tea.Cmd)

	// View renders the page's content (without the outer frame/title).
	View() string

	// KeyMap returns the page's key bindings for the global help component.
	KeyMap() []key.Binding
}
