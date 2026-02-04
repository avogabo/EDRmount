package api

import (
	"fmt"
	"strconv"
	"strings"
)

type byteRange struct {
	Start int64
	End   int64
}

// parseRange parses a single HTTP Range header of form: bytes=start-end
// Supports: bytes=0-499, bytes=500- , bytes=-500 (suffix)
func parseRange(h string, size int64) (*byteRange, error) {
	h = strings.TrimSpace(h)
	if h == "" {
		return nil, nil
	}
	if !strings.HasPrefix(h, "bytes=") {
		return nil, fmt.Errorf("unsupported range")
	}
	spec := strings.TrimPrefix(h, "bytes=")
	// only support single range
	if strings.Contains(spec, ",") {
		return nil, fmt.Errorf("multiple ranges not supported")
	}
	parts := strings.SplitN(spec, "-", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid range")
	}
	startStr := strings.TrimSpace(parts[0])
	endStr := strings.TrimSpace(parts[1])
	var br byteRange
	if startStr == "" {
		// suffix: -N
		n, err := strconv.ParseInt(endStr, 10, 64)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("invalid suffix range")
		}
		if n > size {
			n = size
		}
		br.Start = size - n
		br.End = size - 1
		return &br, nil
	}
	start, err := strconv.ParseInt(startStr, 10, 64)
	if err != nil || start < 0 {
		return nil, fmt.Errorf("invalid range start")
	}
	br.Start = start
	if endStr == "" {
		br.End = size - 1
	} else {
		end, err := strconv.ParseInt(endStr, 10, 64)
		if err != nil || end < 0 {
			return nil, fmt.Errorf("invalid range end")
		}
		br.End = end
	}
	if br.Start >= size {
		return nil, fmt.Errorf("range start out of bounds")
	}
	if br.End >= size {
		br.End = size - 1
	}
	if br.End < br.Start {
		return nil, fmt.Errorf("range end before start")
	}
	return &br, nil
}
