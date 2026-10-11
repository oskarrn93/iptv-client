// Package m3u parses IPTV-style M3U/M3U8 channel playlists.
//
// The parser is streaming: entries are delivered to a callback as they are
// read, so very large playlists never need to be held in memory.
//
// It distinguishes IPTV channel lists (#EXTINF + group-title) from HLS
// playlists (#EXT-X-* tags). An HLS playlist describes a single stream, so
// Parse reports KindHLS and emits no entries; the caller decides how to
// represent it.
package m3u

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
)

// MaxLineLength is the longest line the parser accepts.
const MaxLineLength = 1 << 20

// Kind describes what sort of playlist was parsed.
type Kind int

const (
	KindChannels Kind = iota // IPTV channel list (or plain list of URLs)
	KindHLS                  // HLS master or media playlist: a single stream
)

// Entry is one playable item in a channel playlist.
type Entry struct {
	Name    string
	URL     string
	Group   string
	TvgID   string
	TvgName string
	TvgLogo string
	// Options holds raw #EXTVLCOPT key/value pairs. They come from an
	// untrusted playlist and must be filtered before being passed to a player.
	Options map[string]string
}

// Result summarises a parse.
type Result struct {
	Kind Kind
	// Header holds attributes from the #EXTM3U line (e.g. url-tvg).
	Header  map[string]string
	Entries int
}

// ErrEmpty is returned when the input contains no entries and is not HLS.
var ErrEmpty = errors.New("m3u: playlist contains no entries")

// Parse reads a playlist from r and calls fn for every entry, in order.
// If fn returns an error, parsing stops and that error is returned.
func Parse(r io.Reader, fn func(Entry) error) (Result, error) {
	res := Result{Kind: KindChannels, Header: map[string]string{}}

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), MaxLineLength)

	var (
		pending   *Entry // set by #EXTINF, completed by the URL line
		extGroup  string // last #EXTGRP value; applies until the next entry
		opts      map[string]string
		firstLine = true
		sawNonHLS bool
	)

	for sc.Scan() {
		line := decodeLine(sc.Bytes())
		if firstLine {
			line = strings.TrimPrefix(line, bom)
			firstLine = false
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if line[0] == '#' {
			tag, value := splitTag(line)
			switch strings.ToUpper(tag) {
			case "#EXTM3U":
				_, attrs, _ := parseAttrs(value)
				for k, v := range attrs {
					res.Header[k] = v
				}
			case "#EXTINF":
				sawNonHLS = true
				e := parseExtinf(value)
				pending = &e
			case "#EXTGRP":
				extGroup = strings.TrimSpace(value)
			case "#EXTVLCOPT":
				k, v, ok := strings.Cut(value, "=")
				if ok {
					if opts == nil {
						opts = map[string]string{}
					}
					opts[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
				}
			default:
				// An #EXT-X- tag before any channel entry means this is an
				// HLS playlist, which is one stream rather than a channel list.
				if !sawNonHLS && res.Entries == 0 && strings.HasPrefix(strings.ToUpper(tag), "#EXT-X-") {
					res.Kind = KindHLS
					return res, nil
				}
			}
			continue
		}

		// A non-comment line is a URL that completes the current entry.
		sawNonHLS = true
		e := Entry{}
		if pending != nil {
			e = *pending
		}
		e.URL = line
		if e.Group == "" {
			e.Group = extGroup
		}
		if e.Name == "" {
			e.Name = e.TvgName
		}
		if e.Name == "" {
			e.Name = e.URL
		}
		e.Options = opts

		pending, opts, extGroup = nil, nil, ""
		res.Entries++
		if err := fn(e); err != nil {
			return res, err
		}
	}
	if err := sc.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return res, fmt.Errorf("m3u: line longer than %d bytes", MaxLineLength)
		}
		return res, err
	}
	if res.Entries == 0 {
		return res, ErrEmpty
	}
	return res, nil
}

var bom = string(rune(0xFEFF)) // UTF-8 byte order mark

// splitTag splits "#TAG:value" or "#TAG value" into the tag and the rest.
func splitTag(line string) (tag, value string) {
	i := strings.IndexAny(line, ": \t")
	if i < 0 {
		return line, ""
	}
	return line[:i], line[i+1:]
}

// decodeLine returns the line as a string, treating invalid UTF-8 as
// Windows-1252 (the usual encoding of non-UTF-8 playlists).
func decodeLine(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	s, err := charmap.Windows1252.NewDecoder().Bytes(b)
	if err != nil {
		return strings.ToValidUTF8(string(b), "�")
	}
	return string(s)
}

// parseExtinf parses the part after "#EXTINF:", e.g.
//
//	-1 tvg-id="x" group-title="News",Channel Name
func parseExtinf(s string) Entry {
	title, attrs, _ := parseAttrs(s)
	return Entry{
		Name:    strings.TrimSpace(title),
		Group:   strings.TrimSpace(attrs["group-title"]),
		TvgID:   strings.TrimSpace(attrs["tvg-id"]),
		TvgName: strings.TrimSpace(attrs["tvg-name"]),
		TvgLogo: strings.TrimSpace(attrs["tvg-logo"]),
	}
}

// parseAttrs scans `duration key="value" key=value ...,title`. It returns the
// title (text after the first comma outside quotes), the attributes with
// lowercased keys, and the leading duration token.
func parseAttrs(s string) (title string, attrs map[string]string, duration string) {
	attrs = map[string]string{}
	i, n := 0, len(s)

	skipSpace := func() {
		for i < n && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
	}

	skipSpace()
	// Leading duration token (e.g. -1 or 0), if present.
	start := i
	for i < n && s[i] != ' ' && s[i] != '\t' && s[i] != ',' && s[i] != '=' {
		i++
	}
	if i < n && s[i] == '=' {
		i = start // no duration; first token is an attribute
	} else {
		duration = s[start:i]
	}

	for i < n {
		skipSpace()
		if i >= n {
			break
		}
		if s[i] == ',' {
			return s[i+1:], attrs, duration
		}
		// Key.
		ks := i
		for i < n && s[i] != '=' && s[i] != ' ' && s[i] != '\t' && s[i] != ',' {
			i++
		}
		key := strings.ToLower(s[ks:i])
		if i >= n || s[i] != '=' {
			// Bare word without a value; ignore it.
			continue
		}
		i++ // '='
		var val string
		if i < n && (s[i] == '"' || s[i] == '\'') {
			q := s[i]
			i++
			vs := i
			for i < n && s[i] != q {
				i++
			}
			val = s[vs:i]
			if i < n {
				i++ // closing quote
			}
		} else {
			vs := i
			for i < n && s[i] != ' ' && s[i] != '\t' && s[i] != ',' {
				i++
			}
			val = s[vs:i]
		}
		if key != "" {
			attrs[key] = val
		}
	}
	return "", attrs, duration
}
