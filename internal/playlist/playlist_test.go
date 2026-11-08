package playlist

import (
	"testing"
	"time"
)

func TestDue(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name          string
		p             Playlist
		due, guideDue bool
	}{
		{"never refreshed", Playlist{SourceType: SourceURL, TvgURL: "http://g/"}, true, true},
		{"fresh", Playlist{SourceType: SourceURL, TvgURL: "http://g/", LastRefreshedAt: now.Add(-time.Hour), GuideRefreshedAt: now.Add(-time.Hour)}, false, false},
		{"guide stale only", Playlist{SourceType: SourceURL, TvgURL: "http://g/", LastRefreshedAt: now.Add(-23 * time.Hour), GuideRefreshedAt: now.Add(-12 * time.Hour)}, false, true},
		{"both stale", Playlist{SourceType: SourceURL, GuideURL: "http://g/", LastRefreshedAt: now.Add(-24 * time.Hour), GuideRefreshedAt: now.Add(-13 * time.Hour)}, true, true},
		{"no guide", Playlist{SourceType: SourceURL}, true, false},
		{"file", Playlist{SourceType: SourceFile, TvgURL: "http://g/"}, false, true},
		{"disabled", Playlist{SourceType: SourceURL, TvgURL: "http://g/", Disabled: true}, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.p.Due(now); got != tt.due {
				t.Errorf("Due = %v, want %v", got, tt.due)
			}
			if got := tt.p.GuideDue(now); got != tt.guideDue {
				t.Errorf("GuideDue = %v, want %v", got, tt.guideDue)
			}
		})
	}
}
