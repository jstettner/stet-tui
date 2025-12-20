package main

import "github.com/charmbracelet/lipgloss"

// docStyle is the shared outer frame style for content areas.
// The actual width/height are set dynamically in AppModel.View based on the
// current terminal size (tea.WindowSizeMsg).
var docStyle = lipgloss.NewStyle().Padding(1, 2)
