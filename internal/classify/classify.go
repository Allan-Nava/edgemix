// Package classify puts a request into a class.
//
// A class is a kind of request that costs the same thing to serve: a rendered
// document, a framework navigation, a static asset, an API call. It is the unit
// the whole tool reports in, because an aggregate over all of them answers no
// question — 400k requests an hour is 400k static assets off a CDN or 400k
// renders on six single-threaded processes, and those are different systems.
//
// The default set is the mix a server-rendered site actually produces. Two of
// its rules exist because of measurements that were misread without them:
//
//   - Framework navigation (Next.js React Server Components, and anything else
//     that answers a navigation with a data payload) is *not* a page view. It is
//     often the largest class by count while being the cheapest per request, and
//     averaged into documents it makes the document tier look fast.
//   - Search is kept apart. It is usually the most expensive request per unit,
//     and averaged into the others it hides the thing that falls over first.
package classify

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Allan-Nava/edgemix/internal/logfmt"
)

// Rule matches a request. The zero rule matches nothing: a rule with no
// condition would swallow the stream at whatever position it sits.
type Rule struct {
	Name  string `json:"name"`
	Label string `json:"label,omitempty"`
	// Kind is carried through to an emitted crowdsim profile: "rsc" makes the
	// generator send the framework navigation headers, "plain" a normal request.
	Kind         string   `json:"kind,omitempty"`
	PathPrefixes []string `json:"path_prefixes,omitempty"`
	PathSuffixes []string `json:"path_suffixes,omitempty"`
	PathContains []string `json:"path_contains,omitempty"`
	QueryParams  []string `json:"query_params,omitempty"`
	Methods      []string `json:"methods,omitempty"`
}

// Set is an ordered list of rules plus the class everything else lands in.
// First match wins, which is why the order is data and not a detail: a static
// suffix rule placed before the navigation rule would claim `/logo.svg?_rsc=x`.
type Set struct {
	Rules    []Rule `json:"rules"`
	Fallback string `json:"fallback"`
}

// Default is the built-in set.
func Default() Set {
	return Set{
		Fallback: "doc",
		Rules: []Rule{
			{
				Name: "rsc_nav", Label: "framework navigation", Kind: "rsc",
				QueryParams: []string{"_rsc"},
			},
			{
				Name: "media", Label: "streaming media",
				PathSuffixes: []string{".m3u8", ".mpd", ".ts", ".m4s", ".mp4", ".cmfv", ".cmfa", ".vtt"},
			},
			{
				Name: "static", Label: "static asset",
				PathSuffixes: []string{
					".js", ".mjs", ".css", ".map", ".png", ".jpg", ".jpeg", ".gif",
					".svg", ".webp", ".avif", ".ico", ".woff", ".woff2", ".ttf",
					".eot", ".json", ".txt", ".xml", ".webmanifest",
				},
			},
			{
				Name: "search", Label: "search",
				PathContains: []string{"/search", "/cerca"},
			},
			{
				Name: "api", Label: "API call",
				PathPrefixes: []string{"/api/", "/v1/", "/v2/", "/graphql", "/_next/data/"},
			},
		},
	}
}

// Load reads a set from JSON, validating what a bad set would otherwise turn
// into silently wrong shares.
func Load(data []byte) (Set, error) {
	var s Set
	if err := json.Unmarshal(data, &s); err != nil {
		return Set{}, err
	}
	if strings.TrimSpace(s.Fallback) == "" {
		return Set{}, fmt.Errorf("the set needs a fallback class: without one, requests matching no rule would vanish from the mix")
	}
	seen := map[string]bool{s.Fallback: true}
	for i, r := range s.Rules {
		if strings.TrimSpace(r.Name) == "" {
			return Set{}, fmt.Errorf("rules[%d] has no name", i)
		}
		if seen[r.Name] {
			return Set{}, fmt.Errorf("rules[%d]: class %q appears twice — two rules of one name merge into one share and neither is the one you wrote", i, r.Name)
		}
		seen[r.Name] = true
		if r.Kind != "" && r.Kind != "plain" && r.Kind != "rsc" {
			return Set{}, fmt.Errorf("rules[%d]: kind must be \"plain\" or \"rsc\", not %q", i, r.Kind)
		}
		if len(r.PathPrefixes)+len(r.PathSuffixes)+len(r.PathContains)+len(r.QueryParams) == 0 {
			return Set{}, fmt.Errorf("rules[%d] (%s) has no condition: it would claim every request that reaches it", i, r.Name)
		}
	}
	return s, nil
}

// Classify returns the class of e.
func (s Set) Classify(e logfmt.Event) string {
	for _, r := range s.Rules {
		if r.matches(e) {
			return r.Name
		}
	}
	return s.Fallback
}

// Kind returns the crowdsim kind of a class ("plain" when unset).
func (s Set) Kind(name string) string {
	for _, r := range s.Rules {
		if r.Name == name && r.Kind != "" {
			return r.Kind
		}
	}
	return "plain"
}

// Label returns the human label of a class, or its name.
func (s Set) Label(name string) string {
	for _, r := range s.Rules {
		if r.Name == name && r.Label != "" {
			return r.Label
		}
	}
	return name
}

// Names lists every class the set can produce, rules in order then the
// fallback, so a report lists classes in a stable order rather than by count.
func (s Set) Names() []string {
	out := make([]string, 0, len(s.Rules)+1)
	for _, r := range s.Rules {
		out = append(out, r.Name)
	}
	return append(out, s.Fallback)
}

func (r Rule) matches(e logfmt.Event) bool {
	if len(r.Methods) > 0 {
		ok := false
		for _, m := range r.Methods {
			if strings.EqualFold(m, e.Method) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	path := strings.ToLower(e.Path)
	for _, p := range r.PathPrefixes {
		if strings.HasPrefix(path, strings.ToLower(p)) {
			return true
		}
	}
	for _, sfx := range r.PathSuffixes {
		if strings.HasSuffix(path, strings.ToLower(sfx)) {
			return true
		}
	}
	for _, c := range r.PathContains {
		if strings.Contains(path, strings.ToLower(c)) {
			return true
		}
	}
	for _, q := range r.QueryParams {
		if hasParam(e.Query, q) {
			return true
		}
	}
	return false
}

// hasParam reports whether the raw query string carries the named parameter.
// It matches the name up to `=` or the end, so `_rsc` does not match `_rscx`
// and a valueless `?_rsc` still counts.
func hasParam(query, name string) bool {
	for len(query) > 0 {
		var part string
		if i := strings.IndexAny(query, "&;"); i >= 0 {
			part, query = query[:i], query[i+1:]
		} else {
			part, query = query, ""
		}
		if part == name {
			return true
		}
		if strings.HasPrefix(part, name) && len(part) > len(name) && part[len(name)] == '=' {
			return true
		}
	}
	return false
}
