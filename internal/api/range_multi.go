package api

import (
	"crypto/rand"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"strconv"
	"strings"
)

type multiRange struct {
	Ranges []byteRange
}

func parseRanges(h string, size int64) (*multiRange, error) {
	h = strings.TrimSpace(h)
	if h == "" {
		return nil, nil
	}
	if !strings.HasPrefix(h, "bytes=") {
		return nil, fmt.Errorf("unsupported range")
	}
	spec := strings.TrimPrefix(h, "bytes=")
	parts := strings.Split(spec, ",")
	out := multiRange{Ranges: make([]byteRange, 0, len(parts))}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		br, err := parseRange("bytes="+part, size)
		if err != nil {
			return nil, err
		}
		if br != nil {
			out.Ranges = append(out.Ranges, *br)
		}
	}
	if len(out.Ranges) == 0 {
		return nil, nil
	}
	return &out, nil
}

func randBoundary() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return fmt.Sprintf("edrmount-%x", b)
}

func serveMultiRangeFromFile(w http.ResponseWriter, r *http.Request, f *os.File, size int64, ct string, mr *multiRange) error {
	boundary := randBoundary()
	w.Header().Set("Content-Type", mime.FormatMediaType("multipart/byteranges", map[string]string{"boundary": boundary}))
	w.WriteHeader(http.StatusPartialContent)

	for _, br := range mr.Ranges {
		// part header
		_, _ = io.WriteString(w, "--"+boundary+"\r\n")
		_, _ = io.WriteString(w, fmt.Sprintf("Content-Type: %s\r\n", ct))
		_, _ = io.WriteString(w, fmt.Sprintf("Content-Range: bytes %d-%d/%d\r\n", br.Start, br.End, size))
		_, _ = io.WriteString(w, "\r\n")
		// part body
		if _, err := f.Seek(br.Start, 0); err != nil {
			return err
		}
		if _, err := io.CopyN(w, f, (br.End-br.Start)+1); err != nil {
			return err
		}
		_, _ = io.WriteString(w, "\r\n")
	}
	_, _ = io.WriteString(w, "--"+boundary+"--\r\n")
	return nil
}

func mustAtoi64(s string) int64 {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}
