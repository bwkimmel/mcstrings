// Package log provides logging functions.
package log

import (
	"fmt"
	"os"
)

type Level int

const (
	DebugLevel Level = iota
	InfoLevel
	WarnLevel
	ErrorLevel
)

var minLevel Level = InfoLevel

func SetMinLevel(level Level) {
	minLevel = level
}

func write(level Level, msg string, args ...interface{}) {
	if minLevel > level {
		return
	}
	fmt.Fprintf(os.Stderr, msg, args...)
	fmt.Fprintln(os.Stderr)
}

func Infof(msg string, args ...interface{}) {
	write(InfoLevel, msg, args...)
}

func Info(msg string) {
	Infof(msg)
}

func Debugf(msg string, args ...interface{}) {
	write(DebugLevel, msg, args...)
}

func Debug(msg string) {
	Debugf(msg)
}

func Warnf(msg string, args ...interface{}) {
	write(WarnLevel, msg, args...)
}

func Warn(msg string) {
	Warnf(msg)
}

func Errorf(msg string, args ...interface{}) {
	write(ErrorLevel, msg, args...)
}

func Error(msg string) {
	Errorf(msg)
}
