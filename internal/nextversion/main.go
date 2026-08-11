// Command nextversion works out the version a release should carry, so nobody
// reads the tag list and guesses. Tags arrive on stdin, one per line, and it
// writes GITHUB_OUTPUT lines to stdout.
//
// It sits under internal/ because it is release plumbing for this repository's
// own workflow, with no caller outside it.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

type version struct {
	major, minor, patch int
	rc                  int // 0 when this is not a candidate
}

// bare omits the leading v. Helm rejects it in a chart version, and an OCI tag
// reads better without it, so artifacts carry this and only git carries the v.
func (v version) bare() string {
	s := fmt.Sprintf("%d.%d.%d", v.major, v.minor, v.patch)
	if v.rc > 0 {
		s += fmt.Sprintf("-rc.%d", v.rc)
	}
	return s
}

func (v version) String() string { return "v" + v.bare() }

// release is v with any candidate suffix removed, so a candidate and the
// release it precedes compare as the same line of work.
func (v version) release() version { v.rc = 0; return v }

// less orders versions, with a candidate sorting BEFORE the release it
// precedes: v1.2.0-rc.1 comes before v1.2.0, which is what semver requires and
// what makes "the highest tag" mean the right thing.
func (v version) less(o version) bool {
	switch {
	case v.major != o.major:
		return v.major < o.major
	case v.minor != o.minor:
		return v.minor < o.minor
	case v.patch != o.patch:
		return v.patch < o.patch
	case (v.rc == 0) != (o.rc == 0):
		return v.rc != 0 // a candidate precedes its release
	default:
		return v.rc < o.rc
	}
}

func parse(tag string) (version, bool) {
	var v version
	s := strings.TrimPrefix(strings.TrimSpace(tag), "v")
	if s == "" {
		return v, false
	}
	if base, suffix, found := strings.Cut(s, "-rc."); found {
		n, err := strconv.Atoi(suffix)
		if err != nil || n < 1 {
			return v, false
		}
		v.rc, s = n, base
	} else if strings.Contains(s, "-") {
		return v, false // some other prerelease shape; not ours to reason about
	}

	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return v, false
	}
	fields := []*int{&v.major, &v.minor, &v.patch}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v, false
		}
		*fields[i] = n
	}
	return v, true
}

// latestOf parses tags, drops any it does not recognise, and returns the
// highest. Git prints tags in lexical order, which puts v1.9.0 after v1.10.0.
func latestOf(raw []string) (version, bool) {
	var tags []version
	for _, r := range raw {
		if v, ok := parse(r); ok {
			tags = append(tags, v)
		}
	}
	if len(tags) == 0 {
		return version{}, false
	}
	sort.Slice(tags, func(i, j int) bool { return tags[i].less(tags[j]) })
	return tags[len(tags)-1], true
}

type result struct {
	next     version
	previous string // empty on the very first release
}

// outputs renders the GITHUB_OUTPUT block the workflow reads.
func (r result) outputs() string {
	return fmt.Sprintf("next=%s\nversion=%s\nprevious=%s\nprerelease=%t\n",
		r.next, r.next.bare(), r.previous, r.next.rc > 0)
}

// resolve picks the next version. A candidate continues the line it is on:
// once v1.2.0-rc.1 exists the next is rc.2 for the SAME version, or the
// candidate races ahead of the release it exists to rehearse.
func resolve(raw []string, bump string, candidate bool) (result, error) {
	latest, haveTag := latestOf(raw)
	if !haveTag {
		first := version{minor: 1}
		if candidate {
			first.rc = 1
		}
		return result{next: first}, nil
	}

	out := result{previous: latest.String()}

	switch {
	case candidate && latest.rc > 0:
		latest.rc++
		out.next = latest

	// Promoting a candidate takes its version and ignores the bump: v1.3.0-rc.1
	// IS v1.3.0 in rehearsal, so bumping again would ship a release the
	// candidate never tested and strand v1.3.0 at a version that never happens.
	case latest.rc > 0:
		out.next = latest.release()

	default:
		base := latest.release()
		switch bump {
		case "major":
			base = version{major: base.major + 1}
		case "minor":
			base = version{major: base.major, minor: base.minor + 1}
		case "patch":
			base.patch++
		default:
			return result{}, fmt.Errorf("unknown bump %q", bump)
		}
		if candidate {
			base.rc = 1
		}
		out.next = base
	}
	return out, nil
}

func main() {
	bump := flag.String("bump", "patch", "which part to bump: major, minor or patch")
	candidate := flag.Bool("candidate", false, "cut a release candidate")
	flag.Parse()

	var raw []string
	scan := bufio.NewScanner(os.Stdin)
	for scan.Scan() {
		raw = append(raw, scan.Text())
	}
	if err := scan.Err(); err != nil {
		fail("reading tags: %v", err)
	}

	out, err := resolve(raw, *bump, *candidate)
	if err != nil {
		fail("%v", err)
	}
	fmt.Print(out.outputs())
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "::error::"+format+"\n", args...)
	os.Exit(1)
}
