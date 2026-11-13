// Package logx holds the one logging helper this repository still needs:
// a way to say the same thing log.Fatal said — write the line, then stop —
// without reaching for the standard library logger the gate forbids.
//
// Everything else logs through log/slog directly. This helper exists so the
// exit is visible at the call site and happens in exactly one place.
package logx

import (
	"log/slog"
	"os"
)

// Fatal records msg at error level and terminates the process with status 1,
// the way log.Fatal did. The arguments are slog key/value pairs, so the line
// that precedes an exit is structured like every other line.
func Fatal(msg string, args ...any) {
	slog.Error(msg, args...)
	os.Exit(1)
}
