package subject

import (
	"path/filepath"
	"regexp"
	"strings"
)

var quotedRe = regexp.MustCompile(`"([^"]+)"`)
var extTokenRe = regexp.MustCompile(`(?i)([^\s"<>]+\.(mkv|mp4|avi|m4v|mov|ts|m2ts|wmv|par2|srt|sub|idx|nfo|txt|rar|7z|zip|iso|bin))`)
var filenameSuffixes = []string{".mkv", ".mp4", ".avi", ".m4v", ".mov", ".ts", ".m2ts", ".wmv", ".par2", ".srt", ".sub", ".idx", ".nfo", ".txt", ".rar", ".7z", ".zip", ".iso", ".bin"}

// FilenameFromSubject tries to extract a filename from an NZB subject.
// Supports quoted and unquoted classic forms, e.g.:
// - ... "filename.ext" ...
// - ... filename.ext yEnc (...)
func FilenameFromSubject(subj string) (string, bool) {
	m := quotedRe.FindStringSubmatch(subj)
	if len(m) == 2 {
		name := strings.TrimSpace(m[1])
		if name != "" {
			return filepath.Base(name), true
		}
	}

	s := strings.TrimSpace(subj)
	if i := strings.Index(strings.ToLower(s), " yenc"); i > 0 {
		s = strings.TrimSpace(s[:i])
	}
	for _, suf := range filenameSuffixes {
		if strings.HasSuffix(strings.ToLower(s), suf) {
			return filepath.Base(s), true
		}
	}

	m = extTokenRe.FindStringSubmatch(subj)
	if len(m) >= 2 {
		name := strings.TrimSpace(m[1])
		if name != "" {
			return filepath.Base(name), true
		}
	}
	return "", false
}
