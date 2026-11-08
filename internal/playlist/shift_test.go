package playlist

import "testing"

func TestShiftTimes(t *testing.T) {
	cases := []struct {
		in    string
		hours int
		want  string
	}{
		{"Team A vs Team B 21:45", -1, "Team A vs Team B 20:45"},
		{"Game 9:05", 1, "Game 10:05"},
		{"Game 9:05", -1, "Game 8:05"},
		{"Game 0:30", -1, "Game 23:30"},
		{"Game 23:30", 2, "Game 01:30"},
		{"Game 08:15", 1, "Game 09:15"},
		{"20:00 and 21:45", -1, "19:00 and 20:45"},
		{"(21:45)", 1, "(22:45)"},
		{"21:45", 0, "21:45"},
		{"no times", 3, "no times"},
		{"Ünïcödé 21:45 ✓", -1, "Ünïcödé 20:45 ✓"},
		{"Length 1:23:45", 1, "Length 1:23:45"},
		{"Ratio 21:45:10", 1, "Ratio 21:45:10"},
		{"Score 24:00", 1, "Score 24:00"},
		{"Score 12:60", 1, "Score 12:60"},
		{"Id 121:45", 1, "Id 121:45"},
		{"v1.21:45", 1, "v1.21:45"},
		{"21:451", 1, "21:451"},
		{"9:05 pm", 1, "9:05 pm"},
		{"9:05PM", 1, "9:05PM"},
		{"9:05 Premier League", 1, "10:05 Premier League"},
		{":45 21:", 1, ":45 21:"},
	}
	for _, c := range cases {
		if got := ShiftTimes(c.in, c.hours); got != c.want {
			t.Errorf("ShiftTimes(%q, %d) = %q, want %q", c.in, c.hours, got, c.want)
		}
	}
}
