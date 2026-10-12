package m3u

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func parseFile(t *testing.T, name string) (Result, []Entry, error) {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var got []Entry
	res, err := Parse(f, func(e Entry) error {
		got = append(got, e)
		return nil
	})
	return res, got, err
}

func TestParseFixtures(t *testing.T) {
	tests := []struct {
		file    string
		kind    Kind
		wantErr error
		want    []Entry
		header  map[string]string
	}{
		{
			file:   "basic.m3u",
			header: map[string]string{"url-tvg": "http://epg.example.com/guide.xml"},
			want: []Entry{
				{Name: "BBC One HD", URL: "http://stream.example.com/live/user/pass/1001.ts", Group: "UK", TvgID: "bbc1.uk", TvgName: "BBC One", TvgLogo: "http://logo.example.com/bbc1.png"},
				{Name: "BBC One SD", URL: "http://stream.example.com/live/user/pass/1002.ts", Group: "UK", TvgID: "bbc1.uk", TvgName: "BBC One"},
				{Name: "CNN, International", URL: "https://stream.example.com/cnn.m3u8", Group: "News", TvgID: "cnn.us"},
			},
		},
		{
			file: "bom_crlf.m3u",
			want: []Entry{
				{Name: "Sky Sports", URL: "http://example.com/sky", Group: "Sport"},
				{Name: "Eurosport", URL: "http://example.com/euro", Group: "Sport"},
			},
		},
		{
			file: "latin1.m3u",
			want: []Entry{{Name: "France 2 été", URL: "http://example.com/f2", Group: "Télé"}},
		},
		{
			file: "unquoted.m3u",
			want: []Entry{
				{Name: "Movie Channel", URL: "http://example.com/movie", Group: "Movies", TvgID: "abc", TvgLogo: "http://x/y.png"},
				{Name: "No Attributes", URL: "http://example.com/plain"},
			},
		},
		{
			file: "extgrp.m3u",
			want: []Entry{
				{Name: "MTV", URL: "http://example.com/mtv", Group: "Music"},
				{Name: "Ungrouped After", URL: "http://example.com/after"},
			},
		},
		{
			file: "vlcopt.m3u",
			want: []Entry{
				{Name: "Needs Referrer", URL: "http://example.com/ref", Group: "Ref", Options: map[string]string{
					"http-referrer":   "http://site.example.com/",
					"http-user-agent": "Mozilla/5.0 (X11)",
					// The parser keeps everything; filtering is the player's job.
					"sout": `#file{dst=C:\evil.bat}`,
				}},
				{Name: "No Options", URL: "http://example.com/noopt", Group: "Ref"},
			},
		},
		{file: "hls_master.m3u8", kind: KindHLS},
		{file: "hls_media.m3u8", kind: KindHLS},
		{
			file: "plain_urls.m3u",
			want: []Entry{
				{Name: "http://example.com/a", URL: "http://example.com/a"},
				{Name: "http://example.com/b", URL: "http://example.com/b"},
			},
		},
		{file: "empty.m3u", wantErr: ErrEmpty},
		{
			// Garbage parses as one "URL" entry; URL validation downstream rejects it.
			file: "garbage.m3u",
			want: []Entry{{Name: "\x00\x01garbage<html>not a playlist", URL: "\x00\x01garbage<html>not a playlist"}},
		},
		{
			file: "tvgname_only.m3u",
			want: []Entry{
				{Name: "Fallback Name", URL: "http://example.com/fallback", Group: "X", TvgName: "Fallback Name"},
				{Name: "http://example.com/urlname", URL: "http://example.com/urlname", Group: "X"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			res, got, err := parseFile(t, tt.file)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if res.Kind != tt.kind {
				t.Fatalf("kind = %v, want %v", res.Kind, tt.kind)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("entries mismatch\n got: %+v\nwant: %+v", got, tt.want)
			}
			if res.Entries != len(tt.want) {
				t.Errorf("Entries = %d, want %d", res.Entries, len(tt.want))
			}
			for k, v := range tt.header {
				if res.Header[k] != v {
					t.Errorf("header[%q] = %q, want %q", k, res.Header[k], v)
				}
			}
		})
	}
}

func TestParseCallbackErrorStops(t *testing.T) {
	stop := errors.New("stop")
	n := 0
	_, err := Parse(strings.NewReader("http://a\nhttp://b\nhttp://c\n"), func(Entry) error {
		n++
		return stop
	})
	if !errors.Is(err, stop) || n != 1 {
		t.Fatalf("err=%v n=%d, want stop after 1", err, n)
	}
}

func TestParseLineTooLong(t *testing.T) {
	long := "http://example.com/" + strings.Repeat("a", MaxLineLength)
	_, err := Parse(strings.NewReader(long), func(Entry) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "line longer") {
		t.Fatalf("err = %v, want line-too-long error", err)
	}
}

func TestParseAttrs(t *testing.T) {
	tests := []struct {
		in    string
		title string
		attrs map[string]string
	}{
		{`-1,Simple`, "Simple", map[string]string{}},
		{`-1 a="1" B="2",T`, "T", map[string]string{"a": "1", "b": "2"}},
		{`a="x,y",Title, with comma`, "Title, with comma", map[string]string{"a": "x,y"}},
		{`-1 a="unterminated,T`, "", map[string]string{"a": "unterminated,T"}},
		{`-1 bare a=1,T`, "T", map[string]string{"a": "1"}},
		{``, "", map[string]string{}},
	}
	for _, tt := range tests {
		attrs := map[string]string{}
		title := parseAttrs(tt.in, func(k, v string) { attrs[k] = v })
		if title != tt.title || !reflect.DeepEqual(attrs, tt.attrs) {
			t.Errorf("parseAttrs(%q) = %q, %v; want %q, %v", tt.in, title, attrs, tt.title, tt.attrs)
		}
	}
}

func FuzzParse(f *testing.F) {
	files, _ := filepath.Glob("testdata/*")
	for _, name := range files {
		b, err := os.ReadFile(name)
		if err == nil {
			f.Add(b)
		}
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = Parse(strings.NewReader(string(data)), func(e Entry) error {
			if e.URL == "" {
				t.Fatal("entry with empty URL")
			}
			if e.Name == "" {
				t.Fatal("entry with empty name")
			}
			return nil
		})
	})
}
