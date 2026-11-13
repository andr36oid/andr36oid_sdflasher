package firmware

import (
	"regexp"
	"strings"
)

// These are the coordinator's refusal formats. Keep the arguments separate at
// the presentation boundary so a formatted helper error still gets translated.
var DTBErrorFormats = []string{
	"This set is for build %s. The card has build %s. Use the set that was published with the card's build.",
	"This set has no files for the %s kernel that is on the card.",
	"This set has no files for %s, the console chosen on this card. Nothing was changed.",
	"The first partition of the card has too little free space (%s needed, %s free). Nothing was changed.",
}

type dtbErrorPattern struct {
	key string
	re  *regexp.Regexp
}

var dtbErrorPatterns = func() []dtbErrorPattern {
	out := []dtbErrorPattern{}
	for _, key := range DTBErrorFormats {
		parts := strings.Split(key, "%s")
		pattern := "^" + regexp.QuoteMeta(parts[0])
		for _, part := range parts[1:] {
			pattern += "(.*?)" + regexp.QuoteMeta(part)
		}
		out = append(out, dtbErrorPattern{key, regexp.MustCompile(pattern + "$")})
	}
	return out
}()

func DTBErrorDetails(raw string) (string, []any) {
	if !strings.HasPrefix(raw, "This set ") && !strings.HasPrefix(raw, "The first partition ") {
		return raw, nil
	}
	for _, pattern := range dtbErrorPatterns {
		match := pattern.re.FindStringSubmatch(raw)
		if match == nil {
			continue
		}
		args := make([]any, len(match)-1)
		for n, s := range match[1:] {
			args[n] = s
		}
		return pattern.key, args
	}
	return raw, nil
}
