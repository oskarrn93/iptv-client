// Command iptv-debug exercises the core library without the GUI. It is a
// development tool and is not shipped.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"text/tabwriter"

	"iptv-client/internal/library"
	"iptv-client/internal/store"
)

const usage = `usage: iptv-debug [-db path] <command> [args]

commands:
  add <url|file> [name]          import a playlist
  list                           list playlists
  refresh <playlist-id> [-force] re-import a playlist
  rm <playlist-id>               delete a playlist
  groups <playlist-id>           list groups with counts
  channels <playlist-id> [group] list channels (optionally in one group)
  search <playlist-id> <query>   full-text search
  fav <channel-id> on|off        set favorite
  favs <playlist-id>             list favorites
  play <channel-id>              open in VLC
  vlc                            show the VLC path that would be used
`

func main() {
	defaultDB, _ := store.ResolvePath()
	dbPath := flag.String("db", defaultDB, "database file")
	flag.Usage = func() { fmt.Fprint(os.Stderr, usage); flag.PrintDefaults() }
	flag.Parse()
	if flag.NArg() == 0 {
		flag.Usage()
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	s, err := store.Open(ctx, *dbPath)
	if err != nil {
		fail(err)
	}
	defer s.Close()
	if err := run(ctx, library.New(s), flag.Args()); err != nil {
		s.Close()
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

func run(ctx context.Context, l *library.Library, args []string) error {
	cmd, args := args[0], args[1:]
	need := func(n int) error {
		if len(args) < n {
			return errors.New("missing arguments\n\n" + usage)
		}
		return nil
	}
	id := func(i int) int64 {
		v, err := strconv.ParseInt(args[i], 10, 64)
		if err != nil {
			fail(fmt.Errorf("invalid id %q", args[i]))
		}
		return v
	}
	progress := func(n int) { fmt.Fprintf(os.Stderr, "\r%d channels…", n) }
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	defer w.Flush()

	switch cmd {
	case "add":
		if err := need(1); err != nil {
			return err
		}
		name := ""
		if len(args) > 1 {
			name = args[1]
		}
		p, res, err := l.AddPlaylist(ctx, name, args[0], progress)
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return err
		}
		fmt.Printf("added playlist %d %q: %d channels, %d skipped (unplayable URLs)\n", p.ID, p.Name, res.Imported, res.Skipped)

	case "list":
		ps, err := l.Store.ListPlaylists(ctx)
		if err != nil {
			return err
		}
		fmt.Fprintln(w, "ID\tNAME\tTYPE\tCHANNELS\tREFRESHED")
		for _, p := range ps {
			fmt.Fprintf(w, "%d\t%s\t%s\t%d\t%s\n", p.ID, p.Name, p.SourceType, p.ChannelCount, p.LastRefreshedAt.Format("2006-01-02 15:04"))
		}

	case "refresh":
		if err := need(1); err != nil {
			return err
		}
		force := len(args) > 1 && args[1] == "-force"
		res, err := l.Refresh(ctx, id(0), force, progress)
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return err
		}
		if res.NotModified {
			fmt.Println("not modified")
		} else {
			fmt.Printf("%d channels, %d skipped\n", res.Imported, res.Skipped)
		}

	case "rm":
		if err := need(1); err != nil {
			return err
		}
		return l.Store.DeletePlaylist(ctx, id(0))

	case "groups":
		if err := need(1); err != nil {
			return err
		}
		gs, err := l.Store.ListGroups(ctx, id(0))
		if err != nil {
			return err
		}
		for _, g := range gs {
			name := g.Name
			if name == "" {
				name = "(ungrouped)"
			}
			fmt.Fprintf(w, "%s\t%d\n", name, g.Count)
		}

	case "channels", "search", "favs":
		if err := need(1); err != nil {
			return err
		}
		f := store.ChannelFilter{PlaylistID: id(0), FavoritesOnly: cmd == "favs"}
		if len(args) > 1 {
			if cmd == "search" {
				f.Query = args[1]
			} else if cmd == "channels" {
				f.Group = &args[1]
			}
		}
		return printChannels(ctx, l, f, w)

	case "fav":
		if err := need(2); err != nil {
			return err
		}
		return l.Store.SetFavorite(ctx, id(0), args[1] == "on")

	case "play":
		if err := need(1); err != nil {
			return err
		}
		return l.Play(ctx, id(0))

	case "vlc":
		p, err := l.VLCPath(ctx)
		if err != nil {
			return err
		}
		fmt.Println(p)

	default:
		return fmt.Errorf("unknown command %q\n\n%s", cmd, usage)
	}
	return nil
}

func printChannels(ctx context.Context, l *library.Library, f store.ChannelFilter, w *tabwriter.Writer) error {
	ids, err := l.Store.ChannelIDs(ctx, f)
	if err != nil {
		return err
	}
	cs, err := l.Store.ChannelsByIDs(ctx, ids)
	if err != nil {
		return err
	}
	fmt.Fprintln(w, "ID\tFAV\tGROUP\tNAME")
	for _, c := range cs {
		fav := ""
		if c.Favorite {
			fav = "★"
		}
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\n", c.ID, fav, c.Group, c.Name)
	}
	return nil
}
