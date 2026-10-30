package dashboard

import (
	"sort"
	"strconv"
	"strings"
)

// Owner decision 4 (cycle 9): combo members are ordered by semantic version,
// newest first, comparing every component as a number (4.10 > 4.9). Only
// tokens that this rule defines as versions count; dates, snapshot ids and
// arbitrary suffixes never do.
//
// The rule, applied to the model part of an "alias/model" key (after the last
// "/", with any ":tag" removed), split on "-":
//   - the version is the first token that is "N", "N.N…", "vN.N…", or — when
//     it is not the first token — one letter followed by digits ("k2.6",
//     "r1");
//   - each component has at most 3 digits and no leading zero, so "2024",
//     "2411", "0528" or "08" are not versions; meeting such a token before a
//     version ends the scan and the model is unversioned;
//   - 1–2 digit tokens right after the version are further components
//     ("claude-sonnet-4-5" is 4.5; "gpt-4-0613" stays 4);
//   - tokens glued to letters ("gpt-4o", "o3-mini", "qwen3-coder") are not
//     versions under this rule.
//
// Versioned models sort before unversioned ones; equal versions (missing
// components count as 0) and unversioned models fall back to the key itself,
// so the order is total and deterministic.

func modelVersion(key string) ([]int, bool) {
	model := key
	if i := strings.LastIndex(model, "/"); i >= 0 {
		model = model[i+1:]
	}
	if i := strings.Index(model, ":"); i >= 0 {
		model = model[:i]
	}
	tokens := strings.Split(strings.ToLower(model), "-")
	for i, tok := range tokens {
		var digits string
		switch {
		case versionToken.MatchString(tok):
			digits = strings.TrimPrefix(tok, "v")
		case i > 0 && letterDigitToken.MatchString(tok):
			digits = tok[1:]
		default:
			continue
		}
		parts, ok := versionComponents(digits)
		if !ok {
			return nil, false
		}
		for _, next := range tokens[i+1:] {
			if len(next) > 2 {
				break
			}
			extra, ok := versionComponents(next)
			if !ok || len(extra) != 1 {
				break
			}
			parts = append(parts, extra[0])
		}
		return parts, true
	}
	return nil, false
}

func versionComponents(s string) ([]int, bool) {
	fields := strings.Split(s, ".")
	out := make([]int, 0, len(fields))
	for _, f := range fields {
		if f == "" || len(f) > 3 || (len(f) > 1 && f[0] == '0') {
			return nil, false
		}
		n, err := strconv.Atoi(f)
		if err != nil {
			return nil, false
		}
		out = append(out, n)
	}
	return out, true
}

// compareVersions returns >0 when a is newer than b, <0 when older, 0 when
// equal (missing components count as 0).
func compareVersions(a, b []int) int {
	for i := 0; i < len(a) || i < len(b); i++ {
		var x, y int
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			if x > y {
				return 1
			}
			return -1
		}
	}
	return 0
}

// newerModelFirst is the decision-4 order: versioned before unversioned,
// higher version first, then the key lexicographically.
func newerModelFirst(a, b string) bool {
	va, oka := modelVersion(a)
	vb, okb := modelVersion(b)
	if oka != okb {
		return oka
	}
	if oka {
		if c := compareVersions(va, vb); c != 0 {
			return c > 0
		}
	}
	return a < b
}

func sortModelsByVersion(keys []string) {
	sort.SliceStable(keys, func(i, j int) bool { return newerModelFirst(keys[i], keys[j]) })
}

// sortFreeTierModels groups free models by family (stable, readable list) and
// orders each family newest first.
func sortFreeTierModels(keys []string) {
	family := make(map[string]string, len(keys))
	for _, k := range keys {
		m := k
		if i := strings.LastIndex(m, "/"); i >= 0 {
			m = m[i+1:]
		}
		family[k] = modelFamily(m)
	}
	sort.SliceStable(keys, func(i, j int) bool {
		if fi, fj := family[keys[i]], family[keys[j]]; fi != fj {
			return fi < fj
		}
		return newerModelFirst(keys[i], keys[j])
	})
}
