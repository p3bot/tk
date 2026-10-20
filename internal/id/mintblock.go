package id

// blockedLetterRuns is the mint reject set. A draw is blocked when the letters
// left after stripping digits are equal to one entry. A longer run that only
// contains an entry stays mintable. IsShortID still accepts a blocked draw.
// Extend does not consult this set.
var blockedLetterRuns = map[string]struct{}{
	"anus": {},
	"arse": {},
	"ass":  {},
	"cunt": {},
	"cum":  {},
	"dyk":  {},
	"dyke": {},
	"fag":  {},
	"fuck": {},
	"fuk":  {},
	"gash": {},
	"puss": {},
	"rape": {},
	"shag": {},
	"twat": {},
	"wank": {},
}

// MintBlocked reports whether a minted short id must be drawn again.
// Digits are stripped and the remaining letters are compared to blockedLetterRuns.
func MintBlocked(s string) bool {
	var letters [ShortIDMax]byte
	n := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= '0' && c <= '9' {
			continue
		}
		if n == len(letters) {
			return false
		}
		letters[n] = c
		n++
	}
	_, ok := blockedLetterRuns[string(letters[:n])]
	return ok
}
