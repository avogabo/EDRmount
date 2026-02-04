package nzb

import (
	"encoding/xml"
	"io"
)

// Minimal NZB parser.
// We only need file subjects and segment sizes/ids for now.

type NZB struct {
	Files []File `xml:"file"`
}

type File struct {
	Poster   string    `xml:"poster,attr"`
	Subject  string    `xml:"subject,attr"`
	Date     int64     `xml:"date,attr"`
	Groups   []string  `xml:"groups>group"`
	Segments []Segment `xml:"segments>segment"`
}

type Segment struct {
	Bytes  int64  `xml:"bytes,attr"`
	Number int    `xml:"number,attr"`
	ID     string `xml:",chardata"`
}

func Parse(r io.Reader) (*NZB, error) {
	dec := xml.NewDecoder(r)
	var doc NZB
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	return &doc, nil
}
