package server

import "unicode"

// SafeSessionName returns a screen-safe session name derived from server ID.
func SafeSessionName(serverID string) string {
	base := "hytale-" + serverID
	out := make([]rune, 0, len(base))
	for _, r := range base {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' {
			out = append(out, r)
		} else {
			out = append(out, '-')
		}
	}
	if len(out) == 0 {
		return "hytale-server"
	}
	return string(out)
}
