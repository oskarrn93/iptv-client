package playlist

import "strconv"

// MaxTitleTimeOffset bounds Playlist.TitleTimeOffset, in hours.
const MaxTitleTimeOffset = 23

// ShiftTimes adds hours to every clock time ("21:45", "9:05") in s,
// wrapping around midnight. Providers write kick-off times into titles in
// their own time zone. A time is H:MM or HH:MM with a valid hour and
// minute, not part of a longer number, a longer time ("1:23:45") or a
// 12-hour time ("9:05 pm"). An hour written with two digits keeps them.
func ShiftTimes(s string, hours int) string {
	if hours == 0 {
		return s
	}
	var out []byte
	last := 0 // s[last:] is not copied to out yet
	for i := 0; i < len(s); i++ {
		if s[i] != ':' || i == 0 || !isDigit(s[i-1]) {
			continue
		}
		start := i - 1
		if start > 0 && isDigit(s[start-1]) {
			start--
		}
		if start > 0 && isTimeChar(s[start-1]) {
			continue
		}
		end := i + 3
		if end > len(s) || !isDigit(s[i+1]) || !isDigit(s[i+2]) || (end < len(s) && (isTimeChar(s[end]) || isMeridiem(s[end:]))) {
			continue
		}
		h, _ := strconv.Atoi(s[start:i])
		m, _ := strconv.Atoi(s[i+1 : end])
		if h > 23 || m > 59 {
			continue
		}
		h = ((h+hours)%24 + 24) % 24
		out = append(out, s[last:start]...)
		if i-start == 2 || h >= 10 {
			out = append(out, byte('0'+h/10))
		}
		out = append(out, byte('0'+h%10))
		out = append(out, s[i:end]...)
		last = end
		i = end - 1
	}
	if out == nil {
		return s
	}
	return string(append(out, s[last:]...))
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

// isTimeChar reports whether b next to a time makes it part of something
// else: a longer number, time or a decimal.
func isTimeChar(b byte) bool { return isDigit(b) || b == ':' || b == '.' }

// isMeridiem reports whether s starts with "am" or "pm", maybe after a
// space.
func isMeridiem(s string) bool {
	if len(s) > 0 && s[0] == ' ' {
		s = s[1:]
	}
	if len(s) < 2 || (s[1] != 'm' && s[1] != 'M') {
		return false
	}
	switch s[0] {
	case 'a', 'A', 'p', 'P':
		return true
	}
	return false
}
