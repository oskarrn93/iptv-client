package epg

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"iptv-client/internal/database"
)

const guide = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE tv SYSTEM "xmltv.dtd">
<tv generator-info-name="test">
  <channel id="BBC1.uk">
    <display-name lang="en">BBC One</display-name>
    <display-name>BBC 1</display-name>
    <icon src="http://logos.example/bbc1.png"/>
  </channel>
  <channel id="cnn.us"><display-name>CNN  International</display-name></channel>
  <channel id="other"><display-name>Not In Playlist</display-name></channel>
  <programme start="20261020110000 +0000" stop="20261020120000 +0000" channel="BBC1.uk">
    <title>Ended</title>
  </programme>
  <programme start="20261020120000 +0000" stop="20261020130000 +0000" channel="BBC1.uk">
    <title lang="en">News &amp; Weather&nbsp;Now</title>
    <title lang="de">Nachrichten</title>
    <desc>Headlines.</desc>
    <category>News</category>
  </programme>
  <programme start="20261020150000 +0200" stop="20261020160000 +0200" channel="BBC1.uk">
    <title>Next Show</title>
  </programme>
  <programme start="20261020120000" stop="20261020140000" channel="cnn.us"><title>Live</title></programme>
  <programme start="20261020120000 +0000" stop="20261020130000 +0000" channel="other"><title>Dropped</title></programme>
  <programme start="20261020120000 +0000" stop="20261020130000 +0000" channel="undeclared"><title>By tvg-id</title></programme>
  <programme start="20261020120000 +0000" channel="BBC1.uk"><title>No stop</title></programme>
  <programme start="20261025120000 +0000" stop="20261025130000 +0000" channel="BBC1.uk"><title>Too far ahead</title></programme>
</tv>`

var now = time.Date(2026, 10, 20, 12, 30, 0, 0, time.UTC)

func TestParse(t *testing.T) {
	var chans []Channel
	var progs []Programme
	err := Parse(strings.NewReader(guide), Handler{
		Channel:   func(c Channel) error { chans = append(chans, c); return nil },
		Programme: func(p Programme) error { progs = append(progs, p); return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(chans) != 3 || chans[0].ID != "BBC1.uk" || len(chans[0].Names) != 2 || chans[0].Icon != "http://logos.example/bbc1.png" {
		t.Errorf("channels = %+v", chans)
	}
	if len(progs) != 7 { // "No stop" is skipped
		t.Fatalf("got %d programmes: %+v", len(progs), progs)
	}
	p := progs[1]
	if p.Title != "News & Weather Now" || p.Description != "Headlines." ||
		!p.Start.Equal(time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("programme = %+v", p)
	}
	if !progs[2].Start.Equal(time.Date(2026, 10, 20, 13, 0, 0, 0, time.UTC)) {
		t.Errorf("offset not applied: %v", progs[2].Start)
	}
}

func TestParseGzip(t *testing.T) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write([]byte(guide))
	zw.Close()
	n := 0
	if err := Parse(&buf, Handler{Programme: func(Programme) error { n++; return nil }}); err != nil || n != 7 {
		t.Fatalf("n = %d, err = %v", n, err)
	}
}

func TestParseLatin1(t *testing.T) {
	doc := "<?xml version=\"1.0\" encoding=\"ISO-8859-1\"?><tv><channel id=\"a\"><display-name>Caf\xe9</display-name></channel></tv>"
	var name string
	err := Parse(strings.NewReader(doc), Handler{Channel: func(c Channel) error { name = c.Names[0]; return nil }})
	if err != nil || name != "Café" {
		t.Fatalf("name = %q, err = %v", name, err)
	}
}

func TestParseRejectsOtherDocuments(t *testing.T) {
	for _, doc := range []string{"", "#EXTM3U\nhttp://x/1.ts\n", "<html><body>login</body></html>", "\x1f\x8bnot gzip"} {
		if err := Parse(strings.NewReader(doc), Handler{}); err == nil {
			t.Errorf("%q: no error", doc)
		}
	}
	if err := Parse(strings.NewReader("<html/>"), Handler{}); !errors.Is(err, ErrNotXMLTV) {
		t.Errorf("err = %v, want ErrNotXMLTV", err)
	}
}

func TestParseTime(t *testing.T) {
	cases := map[string]time.Time{
		"20261020123456 +0000": time.Date(2026, 10, 20, 12, 34, 56, 0, time.UTC),
		"20261020123456 -0130": time.Date(2026, 10, 20, 14, 4, 56, 0, time.UTC),
		"202610201234":         time.Date(2026, 10, 20, 12, 34, 0, 0, time.UTC),
		"20261020123456+0100":  time.Date(2026, 10, 20, 11, 34, 56, 0, time.UTC),
	}
	for in, want := range cases {
		if got, ok := parseTime(in); !ok || !got.Equal(want) {
			t.Errorf("parseTime(%q) = %v, %v; want %v", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "2026", "2026102012345", "20261020123456 CET", "abcdefghijklmn"} {
		if _, ok := parseTime(in); ok {
			t.Errorf("parseTime(%q) accepted", in)
		}
	}
}

func openDB(t *testing.T) (*database.DB, int64) {
	t.Helper()
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	res, err := db.ExecContext(ctx, `INSERT INTO playlists (name, source_type, source, created_at) VALUES ('p', 'url', 'http://x/', 0)`)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return db, id
}

func TestImportAndLookup(t *testing.T) {
	ctx := context.Background()
	db, pid := openDB(t)
	repo := NewRepository(db)
	keys := NewKeys([]string{"bbc1.UK", "undeclared"}, []string{"cnn international", "BBC One HD"})
	res, err := repo.Import(ctx, pid, strings.NewReader(guide), keys, now, nil)
	if err != nil {
		t.Fatal(err)
	}
	// BBC by id, CNN by name, "undeclared" by programme; "other" unmatched.
	// Programmes: News, Next Show, Live, By tvg-id.
	if res.Channels != 3 || res.Programmes != 4 {
		t.Errorf("result = %+v", res)
	}

	refs := []Ref{
		{ChannelID: 1, PlaylistID: pid, TvgID: "BBC1.uk", Name: "BBC One HD"},
		{ChannelID: 2, PlaylistID: pid, Name: "CNN International"},
		{ChannelID: 3, PlaylistID: pid, TvgID: "wrong", TvgName: "BBC 1", Name: "BBC One (backup)"},
		{ChannelID: 4, PlaylistID: pid, TvgID: "nope", Name: "Nope"},
		{ChannelID: 5, PlaylistID: pid + 1, TvgID: "BBC1.uk"},
	}
	cur, err := repo.Current(ctx, refs, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(cur) != 3 {
		t.Fatalf("current = %+v", cur)
	}
	if c := cur[1]; c.Icon != "http://logos.example/bbc1.png" || len(c.Programmes) != 1 || c.Programmes[0].Title != "News & Weather Now" {
		t.Errorf("bbc = %+v", c)
	}
	if c := cur[2]; c.Icon != "" || len(c.Programmes) != 1 || c.Programmes[0].Title != "Live" {
		t.Errorf("cnn = %+v", c)
	}
	if c := cur[3]; c.Icon == "" || len(c.Programmes) != 1 {
		t.Errorf("bbc by tvg-name = %+v", c)
	}

	info, ok, err := repo.Upcoming(ctx, refs[0], now, 5)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if len(info.Programmes) != 2 || info.Programmes[0].Description != "Headlines." || info.Programmes[1].Title != "Next Show" {
		t.Errorf("upcoming = %+v", info)
	}
	if _, ok, err := repo.Upcoming(ctx, refs[3], now, 5); ok || err != nil {
		t.Errorf("unmatched channel: ok %v, err %v", ok, err)
	}
}

func TestImportKeepsOldGuideOnFailure(t *testing.T) {
	ctx := context.Background()
	db, pid := openDB(t)
	keys := NewKeys([]string{"bbc1.uk"}, nil)
	if _, err := NewRepository(db).Import(ctx, pid, strings.NewReader(guide), keys, now, nil); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"<tv></tv>", guide[:len(guide)/2] + "<<<", "<html/>"} {
		err := db.InTx(ctx, func(tx database.Querier) error {
			_, err := NewRepository(tx).Import(ctx, pid, strings.NewReader(bad), keys, now, nil)
			return err
		})
		if err == nil {
			t.Fatalf("%q: no error", bad)
		}
		cur, _ := NewRepository(db).Current(ctx, []Ref{{ChannelID: 1, PlaylistID: pid, TvgID: "bbc1.uk"}}, now)
		if len(cur[1].Programmes) != 1 {
			t.Errorf("old guide lost after %q: %+v", bad, cur)
		}
	}
	err := db.InTx(ctx, func(tx database.Querier) error {
		_, err := NewRepository(tx).Import(ctx, pid, strings.NewReader("<tv></tv>"), keys, now, nil)
		return err
	})
	if !errors.Is(err, ErrNoMatches) {
		t.Errorf("err = %v, want ErrNoMatches", err)
	}
}

func FuzzParse(f *testing.F) {
	f.Add(guide)
	f.Add("<tv><programme start='1' stop='2' channel='x'><title>t</title></programme></tv>")
	f.Fuzz(func(t *testing.T, doc string) {
		_ = Parse(strings.NewReader(doc), Handler{
			Channel:   func(Channel) error { return nil },
			Programme: func(Programme) error { return nil },
		})
	})
}
