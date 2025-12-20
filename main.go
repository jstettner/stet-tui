package main

import (
	"database/sql"
	"embed"
	"fmt"
	"log"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/joho/godotenv"
	"github.com/pressly/goose/v3"
	"gopkg.in/natefinch/lumberjack.v2"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var embedMigrations embed.FS

const dbPath = "$HOME/.local/share/stet/data.db"
const logPath = "$HOME/.local/share/stet/debug.log"

func main() {
	// Load .env file (ignore error if not found)
	_ = godotenv.Load()

	fileLogger := log.New(&lumberjack.Logger{
		Filename:   os.ExpandEnv(logPath),
		MaxSize:    5,  // Megabytes before it rotates
		MaxBackups: 3,  // Keep only the 3 most recent old log files
		MaxAge:     28, // Days to keep logs
		Compress:   true,
	}, "APP: ", log.LstdFlags)

	dbPath := os.ExpandEnv(dbPath)

	dir := filepath.Dir(dbPath)

	err := os.MkdirAll(dir, 0755)
	if err != nil {
		log.Fatalf("Could not create directories: %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	goose.SetLogger(&gooseLogger{fileLogger})
	goose.SetBaseFS(embedMigrations)

	if err := goose.SetDialect("sqlite3"); err != nil {
		log.Fatal(err)
	}

	// "migrations" is the folder name inside your project
	if err := goose.Up(db, "migrations"); err != nil {
		log.Fatal(err)
	}

	// Initialize Oura client with credentials from environment
	ouraClient := NewOuraClient(
		os.Getenv("OURA_CLIENT_ID"),
		os.Getenv("OURA_CLIENT_SECRET"),
	)

	// Alt-screen makes this a true full-window TUI (no scrollback spam).
	p := tea.NewProgram(NewAppModel(db, ouraClient), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Println("Error running program:", err)
		os.Exit(1)
	}
}
