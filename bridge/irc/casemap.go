package birc

import (
	"errors"
	"regexp"
	"strings"
	"unicode"

	"github.com/matterbridge-org/matterbridge/bridge/config"
	"golang.org/x/text/secure/precis"
	"golang.org/x/text/unicode/norm"
)

// the current possible Ergo config options for its "casemapping" setting.
// we'll use handleCap in handlers.go to figure out which to use
const (
	CM_ASCII         = "ascii"
	CM_RFC1459       = "rfc1459"
	CM_RFC1459STRICT = "rfc1459-strict"
	CM_PRECIS        = "precis"
	CM_PERMISSIVE    = "permissive"
	CM_UNKNOWN       = "@unknown"
)

var (
	errSanitizerEmpty       = errors.New("sanitizing resulted in empty nick")
	usernameNoCasemapNoBidi = precis.NewIdentifier(precis.FoldWidth, precis.Norm(norm.NFC),
		precis.DisallowEmpty, precis.IgnoreCase)

	sanitizeORIG = func(r rune) rune {
		if strings.ContainsRune("!+%@&#$:'\"?*,.", r) || unicode.IsSpace(r) { // include check for any whitespace
			return '-'
		}

		return r
	}

	isASCII = func(r rune) bool { return r <= unicode.MaxASCII }

	sanitizeASCII = func(r rune) rune {
		if strings.ContainsRune("!+%@&#$:'\"?*,.", r) || unicode.IsSpace(r) || !isASCII(r) { // include check for whitespace and non-ascii
			return '-'
		}

		return r
	}

	allowedPRECIS = usernameNoCasemapNoBidi.Allowed()

	sanitizePRECIS = func(r rune) rune {
		if strings.ContainsRune("!+%@&#$:'\"?*,.", r) || unicode.IsSpace(r) || !allowedPRECIS.Contains(r) {
			return '-'
		}

		return r
	}

	// The below vars and the transformation functions (toPRECIS, toPermissive) have been adapted from:
	// github.com/ergochat/ergo/irc/i18n
	//
	// The main difference is that these are not used for matching, but solely for normalization.
	// The Ergo versions are used for checking validity and for comparisons, and in that
	// process they set all characters to lowercase.
	// These ones ensure validity but leave the case alone.

	// reviving the old ergonomadic nickname regex:
	// in permissive mode, allow arbitrary letters, numbers, punctuation, and symbols
	permissiveCharsRegex = regexp.MustCompile(`^[\pL\pN\pP\pS]*$`)

	rfc1459Replacer       = strings.NewReplacer("[", "{", "]", "}", "\\", "|", "~", "^")
	rfc1459StrictReplacer = strings.NewReplacer("[", "{", "]", "}", "\\", "|")

	errCouldNotStabilize = errors.New("could not stabilize string while casefolding")
)

func (b *Birc) SanitizeNick(msg *config.Message) error {
	cleanednick, err := b.sanitizeNick(msg.Username)
	if err != nil {
		b.Log.Errorf("SanitizeNick on %s for %s failed: %s", msg.Username, b.Account, err)
	}

	b.Log.Debugf("SanitizeNick of %s -> %s", msg.Username, cleanednick)

	msg.Username = cleanednick

	return err
}

// Sanitize nicks for RELAYMSG: replace IRC characters with special meanings with "-"
// This only gets called when UseRelayMsg is set.
// The list of disallowed characters is given here:
// https://github.com/jlu5/ircv3-specifications/blob/master/extensions/relaymsg.md
func (b *Birc) sanitizeNick(nick string) (string, error) {
	var cleaned string
	var folded string

	switch b.Casemapping {
	default:
		b.Log.Debugf("sanitizeNick called with unknown Casemapping setting %s, falling back to ASCII", b.Casemapping)
		fallthrough
	case CM_ASCII:
		cleaned = strings.Map(sanitizeASCII, strings.TrimSpace(nick))
		folded = cleaned
	case CM_RFC1459:
		cleaned = strings.Map(sanitizeASCII, strings.TrimSpace(nick))
		folded = rfc1459Replacer.Replace(cleaned)
	case CM_RFC1459STRICT:
		cleaned = strings.Map(sanitizeASCII, strings.TrimSpace(nick))
		folded = rfc1459StrictReplacer.Replace(cleaned)
	case CM_PRECIS:
		cleaned = strings.Map(sanitizePRECIS, strings.TrimSpace(nick))
		folded = b.toPRECIS(cleaned)
		if folded == CM_UNKNOWN {
			folded = strings.Map(sanitizeASCII, cleaned)
		}
	case CM_PERMISSIVE:
		cleaned = strings.Map(sanitizeORIG, strings.TrimSpace(nick))
		folded = b.toPermissive(cleaned)
		if folded == CM_UNKNOWN {
			cleaned = strings.Map(sanitizePRECIS, cleaned)

			folded = b.toPRECIS(cleaned)
			if folded == CM_UNKNOWN {
				folded = strings.Map(sanitizeASCII, cleaned)
			}
		}
	}

	for strings.Index(folded, "-") == 0 && len(folded) > 1 { // Ergo dislikes dashes as the first char of the nick
		folded = folded[1:]
	}

	if folded == "" {
		return "", errSanitizerEmpty
	}

	return folded, nil
}

func (b *Birc) toPRECIS(str string) string {
	newstr, err := b.iterateFolding(usernameNoCasemapNoBidi, str)
	if err != nil {
		return CM_UNKNOWN
	}

	return newstr
}

func (b *Birc) toPermissive(str string) string {
	// b.Log.Debugf("%s in toPermissive", str)
	if !permissiveCharsRegex.MatchString(str) {
		return CM_UNKNOWN
	}

	str = norm.NFD.String(str)
	// str = cases.Fold().String(str)
	// str = norm.NFD.String(str)
	// b.Log.Debugf("returning %s", str)
	return str
}

// Each pass of PRECIS casefolding is a composition of idempotent operations,
// but not idempotent itself. Therefore, the spec says "do it four times and hope
// it converges" (lolwtf). Golang's PRECIS implementation has a "repeat" option,
// which provides this functionality, but unfortunately it's not exposed publicly.
func (b *Birc) iterateFolding(profile *precis.Profile, oldStr string) (string, error) {
	str := oldStr

	var err error
	// follow the stabilizing rules laid out here:
	// https://tools.ietf.org/html/draft-ietf-precis-7564bis-10.html#section-7
	for i := range 4 {
		str, err = profile.String(str)
		if err != nil {
			b.Log.Debugf("Error in iterateFolding on round %d: %s", i, err)
			return "", err
		}

		if oldStr == str {
			break
		}

		oldStr = str
	}

	if oldStr != str {
		return "", errCouldNotStabilize
	}

	return str, nil
}

// TODO: maybe implement some of the Skeleton functions from ergo to reduce the chances of
// impersonation attacks via confusable characters
