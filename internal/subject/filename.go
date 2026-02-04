package subject

import (
	"regexp"
	"strings"
)

var quotedRe = regexp.MustCompile(`"([^"]+)"`)

// FilenameFromSubject tries to extract a filename from an NZB subject.
// ngpost uses: ... "filename.ext" ...
func FilenameFromSubject(subj string) (string, bool) {
	m := quotedRe.FindStringSubmatch(subj)
	if len(m) == 2 {
		name := strings.TrimSpace(m[1])
		if name != "" {
			return name, true
		}
	}
	return "", false
}
