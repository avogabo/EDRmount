package runner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/gaby/EDRmount/internal/config"
	"github.com/gaby/EDRmount/internal/library"
)

var reFileBotTo = regexp.MustCompile(`(?i)\bto\s+\[(.+)\]`)

// maybeNormalizeWithFileBot returns a synthetic normalized path for naming/layout decisions.
// It does not move/rename input files; uploader still uses the original media path.
func maybeNormalizeWithFileBot(ctx context.Context, cfg config.Config, inputPath string, onLog func(string)) (string, bool, error) {
	rn := cfg.Rename
	if strings.ToLower(strings.TrimSpace(rn.Provider)) != "filebot" || !rn.FileBot.Enabled {
		return inputPath, false, nil
	}
	bin := strings.TrimSpace(rn.FileBot.Binary)
	if bin == "" {
		bin = "/usr/local/bin/filebot"
	}
	if _, err := os.Stat(bin); err != nil {
		return inputPath, false, fmt.Errorf("binary not found: %s", bin)
	}

	g := library.GuessFromFilename(filepath.Base(inputPath))
	format := strings.TrimSpace(rn.FileBot.MovieFormat)
	if g.IsSeries {
		format = strings.TrimSpace(rn.FileBot.SeriesFormat)
	}
	if format == "" {
		if g.IsSeries {
			format = "{n} - {s00e00} - {t}"
		} else {
			format = "{n} ({y})"
		}
	}

	action := strings.TrimSpace(rn.FileBot.Action)
	if action == "" {
		action = "test"
	}
	licensePath := strings.TrimSpace(rn.FileBot.LicensePath)
	if licensePath != "" {
		if _, err := os.Stat(licensePath); err == nil {
			_ = runCommand(ctx, func(line string) {
				if onLog != nil {
					onLog("filebot-license: " + line)
				}
			}, bin, "--license", licensePath)
		}
	}
	db := strings.TrimSpace(rn.FileBot.DB)
	if db == "" {
		db = "TheMovieDB"
	}
	lang := strings.TrimSpace(rn.FileBot.Language)
	if lang == "" {
		lang = "es"
	}

	args := []string{"-rename", inputPath, "--db", db, "--lang", lang, "--format", format, "--action", action}
	var lines []string
	err := runCommand(ctx, func(line string) {
		lines = append(lines, line)
		if onLog != nil {
			onLog("filebot: " + line)
		}
	}, bin, args...)

	candidate := ""
	for i := len(lines) - 1; i >= 0; i-- {
		m := reFileBotTo.FindStringSubmatch(lines[i])
		if len(m) == 2 {
			candidate = strings.TrimSpace(m[1])
			break
		}
	}
	if candidate == "" {
		if err != nil {
			return inputPath, false, err
		}
		return inputPath, false, fmt.Errorf("no rename candidate in output")
	}
	base := filepath.Base(candidate)
	if strings.TrimSpace(base) == "" {
		return inputPath, false, fmt.Errorf("empty normalized base")
	}

	normalized := filepath.Join(filepath.Dir(inputPath), base)
	if strings.EqualFold(filepath.Base(inputPath), base) {
		return inputPath, false, nil
	}
	// Some FileBot setups can emit a valid TEST result but still return non-zero
	// (e.g. locale warnings). If we got a concrete candidate, accept it.
	return normalized, true, nil
}
