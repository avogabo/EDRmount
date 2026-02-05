package library

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var reVar = regexp.MustCompile(`\{([a-zA-Z0-9_]+)(?::([^}]+))?\}`)

// Render applies a small Filebot-style template.
// Supported:
// - {name} string variables
// - {num:00} numeric variables with zero-padding (width = len(format))
func Render(tpl string, vars map[string]string, nums map[string]int) string {
	return reVar.ReplaceAllStringFunc(tpl, func(m string) string {
		sub := reVar.FindStringSubmatch(m)
		if len(sub) < 2 {
			return m
		}
		key := sub[1]
		fmtSpec := ""
		if len(sub) >= 3 {
			fmtSpec = sub[2]
		}

		if fmtSpec != "" {
			if v, ok := nums[key]; ok {
				w := len(fmtSpec)
				if w <= 0 {
					return strconv.Itoa(v)
				}
				return fmt.Sprintf("%0*d", w, v)
			}
		}
		if v, ok := vars[key]; ok {
			return v
		}
		if v, ok := nums[key]; ok {
			return strconv.Itoa(v)
		}
		return ""
	})
}

func CleanPath(p string) string {
	p = strings.ReplaceAll(p, "//", "/")
	p = strings.TrimPrefix(p, "/")
	p = strings.TrimSuffix(p, "/")
	return p
}
