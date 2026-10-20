// Package epg imports XMLTV program guides (EPG) and looks up what is on a
// channel. A guide is untrusted input, like the playlist it belongs to.
package epg

import (
	"bufio"
	"compress/gzip"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"golang.org/x/text/encoding/ianaindex"

	"iptv-client/internal/fetch"
)

// MaxUncompressed caps how much XML is read from a gzip-compressed guide, so
// a small download cannot expand without bound.
const MaxUncompressed = 1 << 30

// Field length caps. Guides sometimes carry very long descriptions; the UI
// shows a few lines.
const (
	maxTitle       = 300
	maxDescription = 2000
	maxIcon        = 2048
)

// Channel is a <channel> element.
type Channel struct {
	ID    string
	Names []string // display names
	Icon  string   // URL, "" if none
}

// Programme is a <programme> element.
type Programme struct {
	Channel     string
	Start, Stop time.Time
	Title       string
	Description string
}

// Handler receives the elements of a guide in document order. Returning an
// error stops parsing.
type Handler struct {
	Channel   func(Channel) error
	Programme func(Programme) error
}

// ErrNotXMLTV is returned when the input is not an XMLTV document.
var ErrNotXMLTV = errors.New("not an XMLTV program guide")

// Parse reads an XMLTV guide from r, which may be gzip-compressed. Channels
// and programmes are passed to h as they are read. Programmes without a
// valid start and stop time are skipped.
func Parse(r io.Reader, h Handler) error {
	br := bufio.NewReader(r)
	if magic, _ := br.Peek(2); len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		zr, err := gzip.NewReader(br)
		if err != nil {
			return fmt.Errorf("program guide: %w", err)
		}
		defer zr.Close()
		r = fetch.Limit(zr, MaxUncompressed)
	} else {
		r = br
	}

	dec := xml.NewDecoder(r)
	// Guides in the wild contain HTML entities and stray markup.
	dec.Strict = false
	dec.AutoClose = xml.HTMLAutoClose
	dec.Entity = xml.HTMLEntity
	dec.CharsetReader = func(label string, in io.Reader) (io.Reader, error) {
		enc, err := ianaindex.IANA.Encoding(label)
		if err != nil || enc == nil {
			return nil, fmt.Errorf("unsupported encoding %q", label)
		}
		return enc.NewDecoder().Reader(in), nil
	}

	root := true
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			if root {
				return ErrNotXMLTV
			}
			return nil
		}
		if err != nil {
			if root {
				return ErrNotXMLTV
			}
			return fmt.Errorf("program guide: %w", err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if root {
			if se.Name.Local != "tv" {
				return ErrNotXMLTV
			}
			root = false
			continue
		}
		switch se.Name.Local {
		case "channel":
			var c xmlChannel
			if err := dec.DecodeElement(&c, &se); err != nil {
				return fmt.Errorf("program guide: %w", err)
			}
			if ch, ok := c.channel(); ok && h.Channel != nil {
				if err := h.Channel(ch); err != nil {
					return err
				}
			}
		case "programme":
			var p xmlProgramme
			if err := dec.DecodeElement(&p, &se); err != nil {
				return fmt.Errorf("program guide: %w", err)
			}
			if pr, ok := p.programme(); ok && h.Programme != nil {
				if err := h.Programme(pr); err != nil {
					return err
				}
			}
		default:
			if err := dec.Skip(); err != nil {
				return fmt.Errorf("program guide: %w", err)
			}
		}
	}
}

type xmlChannel struct {
	ID    string   `xml:"id,attr"`
	Names []string `xml:"display-name"`
	Icons []struct {
		Src string `xml:"src,attr"`
	} `xml:"icon"`
}

func (c xmlChannel) channel() (Channel, bool) {
	ch := Channel{ID: strings.TrimSpace(c.ID)}
	if ch.ID == "" {
		return Channel{}, false
	}
	for _, n := range c.Names {
		if n = strings.TrimSpace(n); n != "" {
			ch.Names = append(ch.Names, n)
		}
	}
	for _, i := range c.Icons {
		if src := strings.TrimSpace(i.Src); src != "" && len(src) <= maxIcon {
			ch.Icon = src
			break
		}
	}
	return ch, true
}

type xmlProgramme struct {
	Start   string   `xml:"start,attr"`
	Stop    string   `xml:"stop,attr"`
	Channel string   `xml:"channel,attr"`
	Titles  []string `xml:"title"`
	Descs   []string `xml:"desc"`
}

// programme converts p. Programmes without a stop time are rare in IPTV
// guides and are skipped rather than guessed from the next start.
func (p xmlProgramme) programme() (Programme, bool) {
	start, ok1 := parseTime(p.Start)
	stop, ok2 := parseTime(p.Stop)
	pr := Programme{
		Channel:     strings.TrimSpace(p.Channel),
		Start:       start,
		Stop:        stop,
		Title:       first(p.Titles, maxTitle),
		Description: first(p.Descs, maxDescription),
	}
	if !ok1 || !ok2 || !stop.After(start) || pr.Channel == "" || pr.Title == "" {
		return Programme{}, false
	}
	return pr, true
}

// first returns the first non-empty value, cut to at most limit runes.
func first(values []string, limit int) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			if r := []rune(v); len(r) > limit {
				v = strings.TrimSpace(string(r[:limit])) + "…"
			}
			return v
		}
	}
	return ""
}

// parseTime parses an XMLTV time: "YYYYMMDDhhmmss +hhmm", where trailing
// fields of the date may be omitted and the offset defaults to UTC.
func parseTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	n := 0
	for n < len(s) && n < 14 && s[n] >= '0' && s[n] <= '9' {
		n++
	}
	if n < 12 || n%2 != 0 {
		return time.Time{}, false
	}
	digits := s[:n] + "000000"[:14-n]
	loc := time.UTC
	if zone := strings.TrimSpace(s[n:]); zone != "" {
		z, err := time.Parse("-0700", zone)
		if err != nil {
			return time.Time{}, false
		}
		loc = z.Location()
	}
	t, err := time.ParseInLocation("20060102150405", digits, loc)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// Key normalizes a channel ID or name for matching playlist channels to
// guide channels: case and repeated spaces are ignored.
func Key(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}
