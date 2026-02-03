package runner

import "strings"

// sanitizeLine removes obvious secrets from logs.
// This is intentionally minimal; we can improve it once ngpost output is observed.
func sanitizeLine(line, pass string) string {
	if pass != "" {
		line = strings.ReplaceAll(line, pass, "***")
	}
	return line
}
