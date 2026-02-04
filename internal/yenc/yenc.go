package yenc

import (
	"errors"
	"strconv"
	"strings"
)

// DecodePart decodes yEnc payload lines into bytes.
// It expects to see =ybegin and =yend, optionally =ypart.
// Returns decoded bytes and the declared (begin,end) if present; begin/end are 1-based inclusive.
func DecodePart(lines []string) (data []byte, begin int, end int, name string, err error) {
	begin = 0
	end = 0
	in := false
	for _, l := range lines {
		if strings.HasPrefix(l, "=ybegin") {
			in = true
			// parse name=...
			if i := strings.Index(l, " name="); i >= 0 {
				name = strings.TrimSpace(l[i+6:])
			}
			continue
		}
		if !in {
			continue
		}
		if strings.HasPrefix(l, "=ypart") {
			// parse begin/end
			// =ypart begin=1 end=716800
			fields := strings.Fields(l)
			for _, f := range fields {
				if strings.HasPrefix(f, "begin=") {
					begin, _ = strconv.Atoi(strings.TrimPrefix(f, "begin="))
				}
				if strings.HasPrefix(f, "end=") {
					end, _ = strconv.Atoi(strings.TrimPrefix(f, "end="))
				}
			}
			continue
		}
		if strings.HasPrefix(l, "=yend") {
			return data, begin, end, name, nil
		}

		// payload line
		decoded := decodeLine(l)
		data = append(data, decoded...)
	}
	return nil, 0, 0, name, errors.New("invalid yenc: missing yend")
}

func decodeLine(l string) []byte {
	out := make([]byte, 0, len(l))
	b := []byte(l)
	for i := 0; i < len(b); i++ {
		c := b[i]
		if c == '=' {
			if i+1 < len(b) {
				i++
				c = b[i] - 64
			}
		}
		out = append(out, (c - 42))
	}
	return out
}
