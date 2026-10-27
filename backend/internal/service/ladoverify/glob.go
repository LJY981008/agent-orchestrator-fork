package ladoverify

// Matching of the plan's affected_paths and the protected globs (decision g
// of the step 8 plan): path.Match per segment, a "**" segment for zero or
// more segments, and a pattern without a wildcard for that path and
// everything below it.

import (
	"path"
	"slices"
	"strings"
)

// wildcards are the characters path.Match reads specially.
const wildcards = "*?["

// MatchPath reports whether the slash-separated path p matches pattern. Each
// segment of the pattern matches one segment of p by path.Match, and a "**"
// segment zero or more of them; a pattern without a wildcard matches that
// path and every path below it. Empty and "." segments of the pattern do not
// count, and a malformed pattern matches nothing.
func MatchPath(pattern, p string) bool {
	segments := patternSegments(pattern)
	if len(segments) == 0 {
		return false
	}
	if !strings.ContainsAny(pattern, wildcards) {
		dir := strings.Join(segments, "/")
		return p == dir || strings.HasPrefix(p, dir+"/")
	}
	for _, segment := range segments {
		if _, err := path.Match(segment, ""); err != nil {
			return false
		}
	}
	names := strings.Split(p, "/")
	// reach[i] reports whether the segments so far match names[:i]; one pass
	// per segment keeps a pattern of many "**" linear in the path.
	reach := make([]bool, len(names)+1)
	reach[0] = true
	for _, segment := range segments {
		next := make([]bool, len(names)+1)
		if segment == "**" {
			// Zero or more segments: every name from the first one reached on.
			if first := slices.Index(reach, true); first >= 0 {
				for j := first; j <= len(names); j++ {
					next[j] = true
				}
			}
		} else {
			for i, name := range names {
				if reach[i] {
					next[i+1], _ = path.Match(segment, name)
				}
			}
		}
		reach = next
	}
	return reach[len(names)]
}

// fixedPrefix returns the part of pattern before its first wildcard, without
// the pattern's empty and "." segments. A pattern without a wildcard covers
// its folder as "dir/**" does, so its fixed part ends with a slash.
func fixedPrefix(pattern string) string {
	clean := strings.Join(patternSegments(pattern), "/")
	if i := strings.IndexAny(clean, wildcards); i >= 0 {
		return clean[:i]
	}
	return clean + "/"
}

// patternSegments splits pattern at its slashes, leaving out empty and "."
// segments.
func patternSegments(pattern string) []string {
	var segments []string
	for segment := range strings.SplitSeq(pattern, "/") {
		if segment != "" && segment != "." {
			segments = append(segments, segment)
		}
	}
	return segments
}
