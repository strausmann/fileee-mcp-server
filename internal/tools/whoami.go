package tools

import "strings"

// maskUsername masks a fileee login (an e-mail address) so a caller can
// recognise their own account without the full value appearing in output.
// It never returns the password or TOTP seed — only a reduced form of the
// username.
func maskUsername(username string) string {
	if username == "" {
		return ""
	}
	if at := strings.IndexByte(username, '@'); at >= 0 {
		return maskPart(username[:at]) + username[at:]
	}
	return maskPart(username)
}

func maskPart(s string) string {
	switch {
	case len(s) == 0:
		return ""
	case len(s) == 1:
		return "*"
	default:
		return string(s[0]) + "***" + string(s[len(s)-1])
	}
}
