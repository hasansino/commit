package commit

import "testing"

func TestParseSemVer(t *testing.T) {
	tests := []struct {
		version string
		want    semVer
	}{
		{version: "v1.2.3", want: semVer{Major: 1, Minor: 2, Patch: 3}},
		{version: "1.2.3", want: semVer{Major: 1, Minor: 2, Patch: 3}},
		{version: "v0.0.0", want: semVer{}},
		{version: "invalid", want: semVer{}},
		{version: "v1.2", want: semVer{}},
		{version: "v999999999999999999999999.0.0", want: semVer{}},
	}
	for _, test := range tests {
		t.Run(test.version, func(t *testing.T) {
			if got := parseSemVer(test.version); got != test.want {
				t.Fatalf("parseSemVer(%q) = %+v, want %+v", test.version, got, test.want)
			}
		})
	}
}

func TestIncrementVersion(t *testing.T) {
	tests := []struct {
		name      string
		current   string
		increment string
		want      string
		wantErr   bool
	}{
		{name: "first patch", increment: "patch", want: "v0.0.1"},
		{name: "patch", current: "v1.2.3", increment: "PATCH", want: "v1.2.4"},
		{name: "minor", current: "v1.2.3", increment: "minor", want: "v1.3.0"},
		{name: "major", current: "v1.2.3", increment: "major", want: "v2.0.0"},
		{
			name:      "arbitrary precision",
			current:   "v999999999999999999999999.2.3",
			increment: "major",
			want:      "v1000000000000000000000000.0.0",
		},
		{name: "invalid version", current: "release-1", increment: "patch", wantErr: true},
		{name: "invalid increment", current: "v1.2.3", increment: "other", wantErr: true},
	}
	g := &gitOperations{}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := g.IncrementVersion(test.current, test.increment)
			if (err != nil) != test.wantErr {
				t.Fatalf("IncrementVersion() error = %v, wantErr %v", err, test.wantErr)
			}
			if got != test.want {
				t.Fatalf("IncrementVersion() = %q, want %q", got, test.want)
			}
		})
	}
}
