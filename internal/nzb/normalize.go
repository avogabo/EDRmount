package nzb

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type canonicalNZB struct {
	XMLName xml.Name           `xml:"http://www.newzbin.com/DTD/2003/nzb nzb"`
	Files   []canonicalNZBFile `xml:"file"`
}

type canonicalNZBFile struct {
	Poster   string                 `xml:"poster,attr,omitempty"`
	Subject  string                 `xml:"subject,attr,omitempty"`
	Date     int64                  `xml:"date,attr,omitempty"`
	Groups   []string               `xml:"groups>group,omitempty"`
	Segments []canonicalNZBSegment  `xml:"segments>segment,omitempty"`
}

type canonicalNZBSegment struct {
	Bytes  int64  `xml:"bytes,attr,omitempty"`
	Number int    `xml:"number,attr,omitempty"`
	ID     string `xml:",chardata"`
}

var quotedNameRe = regexp.MustCompile(`"([^"]+\.[A-Za-z0-9]+)"`)
var tokenNameRe = regexp.MustCompile(`(?i)([^\s"<>]+\.(par2|mkv|mp4|avi|m4v|mov|ts|m2ts|wmv|srt|sub|idx|nfo|txt|rar|7z|zip|iso|bin))`)

func canonicalSubject(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return raw
	}
	name := ""
	if m := quotedNameRe.FindStringSubmatch(raw); len(m) == 2 {
		name = strings.TrimSpace(m[1])
	} else if m := tokenNameRe.FindStringSubmatch(raw); len(m) >= 2 {
		name = strings.TrimSpace(m[1])
	} else {
		name = raw
	}
	name = filepath.Base(name)
	if name == "" {
		name = raw
	}
	return `"` + name + `" yEnc (1/1)`
}

// NormalizeCanonical rewrites NZB into a deterministic classic form:
// - classic namespace (<nzb xmlns="...">)
// - stable segment IDs (trimmed)
// - canonical subject format compatible with repair tooling
func NormalizeCanonical(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	doc, err := Parse(f)
	_ = f.Close()
	if err != nil {
		return err
	}

	out := canonicalNZB{Files: make([]canonicalNZBFile, 0, len(doc.Files))}
	for _, nf := range doc.Files {
		cf := canonicalNZBFile{
			Poster:   nf.Poster,
			Subject:  canonicalSubject(nf.Subject),
			Date:     nf.Date,
			Groups:   nf.Groups,
			Segments: make([]canonicalNZBSegment, 0, len(nf.Segments)),
		}
		for _, s := range nf.Segments {
			cf.Segments = append(cf.Segments, canonicalNZBSegment{Bytes: s.Bytes, Number: s.Number, ID: strings.TrimSpace(s.ID)})
		}
		out.Files = append(out.Files, cf)
	}

	xmlBytes, err := xml.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	s := string(xmlBytes)
	s = strings.ReplaceAll(s, "<ns0:", "<")
	s = strings.ReplaceAll(s, "</ns0:", "</")
	s = strings.ReplaceAll(s, "xmlns:ns0=", "xmlns=")
	xmlBytes = append([]byte(xml.Header), []byte(s)...)
	tmp := path + ".canonical.tmp"
	if err := os.WriteFile(tmp, xmlBytes, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
