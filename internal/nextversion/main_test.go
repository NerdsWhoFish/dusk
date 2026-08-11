package main

import "testing"

// The whole GITHUB_OUTPUT block is asserted rather than the parsed struct: it
// is what the workflow actually consumes, so a rename of an output key is a
// break the test should catch.
func TestResolve(t *testing.T) {
	tests := []struct {
		name      string
		tags      []string
		bump      string
		candidate bool
		want      string
	}{
		{
			name: "first release when no tags exist",
			bump: "patch",
			want: "next=v0.1.0\nversion=0.1.0\nprevious=\nprerelease=false\n",
		},
		{
			name:      "first candidate when no tags exist",
			bump:      "minor",
			candidate: true,
			want:      "next=v0.1.0-rc.1\nversion=0.1.0-rc.1\nprevious=\nprerelease=true\n",
		},
		{
			name: "patch bump",
			tags: []string{"v1.2.3"},
			bump: "patch",
			want: "next=v1.2.4\nversion=1.2.4\nprevious=v1.2.3\nprerelease=false\n",
		},
		{
			name: "minor bump zeroes the patch",
			tags: []string{"v1.2.3"},
			bump: "minor",
			want: "next=v1.3.0\nversion=1.3.0\nprevious=v1.2.3\nprerelease=false\n",
		},
		{
			name: "major bump zeroes minor and patch",
			tags: []string{"v1.2.3"},
			bump: "major",
			want: "next=v2.0.0\nversion=2.0.0\nprevious=v1.2.3\nprerelease=false\n",
		},
		{
			// Git lists tags lexically, which puts v1.9.0 last.
			name: "highest tag is numeric, not lexical",
			tags: []string{"v1.10.0", "v1.9.0", "v1.2.0"},
			bump: "patch",
			want: "next=v1.10.1\nversion=1.10.1\nprevious=v1.10.0\nprerelease=false\n",
		},
		{
			name:      "candidate continues its own line",
			tags:      []string{"v1.2.3", "v1.3.0-rc.1"},
			bump:      "minor",
			candidate: true,
			want:      "next=v1.3.0-rc.2\nversion=1.3.0-rc.2\nprevious=v1.3.0-rc.1\nprerelease=true\n",
		},
		{
			name:      "candidate numbering is numeric past rc.9",
			tags:      []string{"v1.3.0-rc.9", "v1.3.0-rc.10"},
			bump:      "minor",
			candidate: true,
			want:      "next=v1.3.0-rc.11\nversion=1.3.0-rc.11\nprevious=v1.3.0-rc.10\nprerelease=true\n",
		},
		{
			// The candidate rehearsed v1.3.0, so that is what ships.
			name: "promoting a candidate ignores the bump",
			tags: []string{"v1.2.3", "v1.3.0-rc.2"},
			bump: "major",
			want: "next=v1.3.0\nversion=1.3.0\nprevious=v1.3.0-rc.2\nprerelease=false\n",
		},
		{
			name:      "new candidate off a plain release",
			tags:      []string{"v1.2.3"},
			bump:      "minor",
			candidate: true,
			want:      "next=v1.3.0-rc.1\nversion=1.3.0-rc.1\nprevious=v1.2.3\nprerelease=true\n",
		},
		{
			// A release always outranks the candidates that preceded it.
			name: "release outranks its own candidates",
			tags: []string{"v1.3.0-rc.1", "v1.3.0-rc.2", "v1.3.0"},
			bump: "patch",
			want: "next=v1.3.1\nversion=1.3.1\nprevious=v1.3.0\nprerelease=false\n",
		},
		{
			name: "unrecognised tags are ignored",
			tags: []string{"", "  v1.2.3  ", "nightly", "v1.2", "v1.2.3.4", "v9.9.9-beta.1", "vx.y.z"},
			bump: "patch",
			want: "next=v1.2.4\nversion=1.2.4\nprevious=v1.2.3\nprerelease=false\n",
		},
		{
			name: "only unrecognised tags is the same as no tags",
			tags: []string{"nightly", "latest"},
			bump: "patch",
			want: "next=v0.1.0\nversion=0.1.0\nprevious=\nprerelease=false\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolve(tt.tags, tt.bump, tt.candidate)
			if err != nil {
				t.Fatalf("resolve(%q, %q, %t) returned error: %v", tt.tags, tt.bump, tt.candidate, err)
			}
			if got.outputs() != tt.want {
				t.Errorf("outputs =\n%q\nwant\n%q", got.outputs(), tt.want)
			}
		})
	}
}

func TestResolveRejectsUnknownBump(t *testing.T) {
	tests := []struct {
		name string
		bump string
	}{
		{name: "empty", bump: ""},
		{name: "misspelled", bump: "Patch"},
		{name: "not a bump at all", bump: "release"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := resolve([]string{"v1.2.3"}, tt.bump, false); err == nil {
				t.Errorf("resolve with bump %q succeeded, want an error", tt.bump)
			}
		})
	}
}

// Promotion bypasses the bump switch entirely, so a bad bump must not fail a
// run that never uses it.
func TestResolveIgnoresBumpWhenPromoting(t *testing.T) {
	got, err := resolve([]string{"v1.3.0-rc.1"}, "nonsense", false)
	if err != nil {
		t.Fatalf("resolve returned error: %v", err)
	}
	if want := "next=v1.3.0\nversion=1.3.0\nprevious=v1.3.0-rc.1\nprerelease=false\n"; got.outputs() != want {
		t.Errorf("outputs = %q, want %q", got.outputs(), want)
	}
}
