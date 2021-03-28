// Package log provides logging functions.
package log

import (
	"fmt"
	"os"
)

// Level is the severity level of a log message.
type Level int

const (
	DebugLevel Level = iota
	InfoLevel
	WarnLevel
	ErrorLevel
)

// minLevel is the minimum level to include in the logging output.
var minLevel Level = InfoLevel

// SetMinLevel sets the minimum level to include in the logging output.
func SetMinLevel(level Level) {
	minLevel = level
}

// write prints a message for the given severity level.
func write(level Level, msg string, args ...interface{}) {
	if minLevel > level {
		return
	}
	fmt.Fprintf(os.Stderr, msg, args...)
	fmt.Fprintln(os.Stderr)
}

// Infof formats an informational log message.
func Infof(msg string, args ...interface{}) {
	write(InfoLevel, msg, args...)
}

// Info prints an informational log message.
func Info(msg string) {
	Infof(msg)
}

// Debugf formats a debugging log message.
func Debugf(msg string, args ...interface{}) {
	write(DebugLevel, msg, args...)
}

// Debug prints a debugging log message.
func Debug(msg string) {
	Debugf(msg)
}

// Warnf formats a warning log message.
func Warnf(msg string, args ...interface{}) {
	write(WarnLevel, msg, args...)
}

// Warn prints a warning log message.
func Warn(msg string) {
	Warnf(msg)
}

// Errorf formats an error log message.
func Errorf(msg string, args ...interface{}) {
	write(ErrorLevel, msg, args...)
}

// Error prints an error log message.
func Error(msg string) {
	Errorf(msg)
}
