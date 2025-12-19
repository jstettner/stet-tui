package main

import "log"

// gooseLogger adapts a standard library log.Logger to Goose's logger interface.
// Goose expects a logger with Printf and Fatalf methods.
type gooseLogger struct {
	l *log.Logger
}

func (c *gooseLogger) Printf(format string, v ...interface{}) {
	if c == nil || c.l == nil {
		log.Printf(format, v...)
		return
	}
	c.l.Printf(format, v...)
}

func (c *gooseLogger) Fatalf(format string, v ...interface{}) {
	if c == nil || c.l == nil {
		log.Fatalf(format, v...)
		return
	}
	c.l.Fatalf(format, v...)
}
