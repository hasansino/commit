package commit

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func replacementDiff(name string, hunks, removed, added int) string {
	var diff strings.Builder
	fmt.Fprintf(&diff, "diff --git %s %s\n--- %s\n+++ %s\n", name, name, name, name)
	for hunk := range hunks {
		fmt.Fprintf(&diff, "@@ -%d,%d +%d,%d @@\n", 1+hunk*10000, removed, 1+hunk*10000, added)
		for line := range removed {
			fmt.Fprintf(&diff, "-old_%s_%d_%04d_complete\n", name, hunk, line)
		}
		for line := range added {
			fmt.Fprintf(&diff, "+new_%s_%d_%04d_complete\n", name, hunk, line)
		}
	}
	return diff.String()
}

func requireCompactDiff(t *testing.T, diff string, limit int) string {
	t.Helper()
	got, err := compactUnifiedDiff(diff, limit)
	if err != nil {
		t.Fatalf("compactUnifiedDiff() error = %v", err)
	}
	if len(got) > limit || !utf8.ValidString(got) {
		t.Fatalf("invalid excerpt: size %d (limit %d), valid UTF-8 %t", len(got), limit, utf8.ValidString(got))
	}
	return got
}

func TestCompactUnifiedDiff(t *testing.T) {
	t.Run("keeps a small diff unchanged", func(t *testing.T) {
		diff := replacementDiff("small", 1, 1, 1)
		if got := requireCompactDiff(t, diff, len(diff)); got != diff {
			t.Fatalf("small diff changed: %q", got)
		}
	})

	t.Run("large replacement retains both sides at the default limit", func(t *testing.T) {
		const lines = 3000
		got := requireCompactDiff(t, replacementDiff("large", 1, lines, lines), 64*1024)
		for _, marker := range []string{
			diffExcerptNotice,
			"-old_large_0_0000_complete\n", "-old_large_0_2999_complete\n",
			"+new_large_0_0000_complete\n", "+new_large_0_2999_complete\n",
			"[File totals: +3000 -3000 lines; omitted hunks: 0/1]",
		} {
			if !strings.Contains(got, marker) {
				t.Errorf("excerpt omitted %q", marker)
			}
		}
		removed := strings.Count(got, "\n-old_")
		added := strings.Count(got, "\n+new_")
		omission := fmt.Sprintf("[Omitted lines: %d removed, %d added;", lines-removed, lines-added)
		if !strings.Contains(got, omission) {
			t.Errorf("incorrect omission counts, want %q", omission)
		}
	})

	t.Run("shares the limit across files and hunks", func(t *testing.T) {
		var diff strings.Builder
		for file := range 4 {
			diff.WriteString(replacementDiff(fmt.Sprintf("file%d", file), 3, 100, 100))
		}
		got := requireCompactDiff(t, diff.String(), 6*1024)
		for file := range 4 {
			for hunk := range 3 {
				for _, side := range []string{"-old", "+new"} {
					marker := fmt.Sprintf("%s_file%d_%d_", side, file, hunk)
					if !strings.Contains(got, marker) {
						t.Errorf("excerpt omitted %q", marker)
					}
				}
			}
		}
	})

	t.Run("preserves small sections and redistributes their allowance", func(t *testing.T) {
		small := replacementDiff("small", 1, 1, 1)
		got := requireCompactDiff(t, small+replacementDiff("large", 1, 100, 100), 2048)
		if !strings.Contains(got, small) {
			t.Fatal("small file diff was shortened")
		}
		if len(got) < 1800 {
			t.Fatalf("excerpt used only %d bytes out of 2048", len(got))
		}
	})

	for _, counts := range [][2]int{{0, 200}, {200, 0}, {2, 200}, {200, 2}} {
		t.Run(fmt.Sprintf("removed %d added %d", counts[0], counts[1]), func(t *testing.T) {
			got := requireCompactDiff(t, replacementDiff("file", 1, counts[0], counts[1]), 1024)
			if counts[0] > 0 && !strings.Contains(got, "-old_file_0_0000_complete\n") {
				t.Error("removed content is missing")
			}
			if counts[1] > 0 && !strings.Contains(got, "+new_file_0_0000_complete\n") {
				t.Error("added content is missing")
			}
			if counts[0] == 2 && !strings.Contains(got, "-old_file_0_0001_complete\n") {
				t.Error("small removed side was unnecessarily shortened")
			}
			if counts[1] == 2 && !strings.Contains(got, "+new_file_0_0001_complete\n") {
				t.Error("small added side was unnecessarily shortened")
			}
		})
	}

	t.Run("keeps binary rename and mode metadata intact", func(t *testing.T) {
		metadata := "diff --git old new\nsimilarity index 100%\nrename from old\nrename to new\n" +
			"diff --git binary binary\nBinary files binary and binary differ\n" +
			"diff --git script script\nold mode 100644\nnew mode 100755\n"
		got := requireCompactDiff(t, metadata+replacementDiff("large", 1, 100, 100), 2048)
		if !strings.Contains(got, metadata) {
			t.Fatal("file metadata was altered or omitted")
		}
	})

	t.Run("skips long lines without cutting Unicode or ordinary lines", func(t *testing.T) {
		long := "+" + strings.Repeat("界", 1000) + "\n"
		diff := "diff --git f f\n--- f\n+++ f\n@@ -1,200 +1,201 @@\n" +
			strings.Repeat("-ancien café 完整\n", 200) + long + strings.Repeat("+nouveau café 完整\n", 200)
		got := requireCompactDiff(t, diff, 1024)
		for _, line := range strings.Split(got, "\n") {
			if strings.HasPrefix(line, "-ancien") && line != "-ancien café 完整" ||
				strings.HasPrefix(line, "+nouveau") && line != "+nouveau café 完整" {
				t.Errorf("partial line: %q", line)
			}
		}
		if strings.Contains(got, "+界") || !strings.Contains(got, "+nouveau café 完整\n") {
			t.Fatal("long line was cut or prevented shorter additions from being shown")
		}
	})

	t.Run("keeps end of file markers attached to sampled lines", func(t *testing.T) {
		diff := replacementDiff("f", 1, 100, 99) + "+last\n\\ No newline at end of file\n"
		got := requireCompactDiff(t, diff, 1024)
		if !strings.Contains(got, "+last\n\\ No newline at end of file\n") {
			t.Fatal("end of file marker detached from its added line")
		}
	})

	t.Run("accounts for omitted files and hunks", func(t *testing.T) {
		var diff strings.Builder
		for i := range 20 {
			diff.WriteString(replacementDiff(fmt.Sprintf("f%02d", i), 20, 50, 50))
		}
		got := requireCompactDiff(t, diff.String(), 1024)
		shown := strings.Count(got, "diff --git ")
		if shown == 0 || shown == 20 || !strings.Contains(got, fmt.Sprintf("[Omitted files: %d]", 20-shown)) {
			t.Fatalf("incorrect file omissions: %s", got)
		}
		if !strings.Contains(got, "omitted hunks: 20/20") {
			t.Fatal("unshown hunks were not reported")
		}
	})

	t.Run("rejects zero negative and unusably small limits", func(t *testing.T) {
		for _, limit := range []int{-1, 0, 1, 64} {
			got, err := compactUnifiedDiff(replacementDiff("f", 1, 100, 100), limit)
			if err == nil || got != "" {
				t.Errorf("limit %d: got (%q, %v), want an error and no excerpt", limit, got, err)
			}
		}
	})
}

func FuzzCompactUnifiedDiff(f *testing.F) {
	f.Add(replacementDiff("f", 2, 20, 20), uint16(512))
	f.Add("diff --git f f\n@@ -1 +1 @@\n-old\n+\xffnew\n", uint16(30))
	f.Add("diff --git f f\nold mode 100644\nnew mode 100755\n", uint16(100))
	f.Fuzz(func(t *testing.T, diff string, size uint16) {
		limit := int(size)
		got, err := compactUnifiedDiff(diff, limit)
		if err != nil {
			if got != "" {
				t.Fatal("an error returned a partial diff")
			}
			return
		}
		if len(got) > limit || !utf8.ValidString(got) {
			t.Fatalf("invalid output: %d bytes (limit %d), UTF-8 %t", len(got), limit, utf8.ValidString(got))
		}
		if limit > 0 && utf8.ValidString(diff) && len(diff) <= limit && got != diff {
			t.Fatal("a fitting diff was changed")
		}
	})
}
