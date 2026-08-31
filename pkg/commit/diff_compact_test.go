package commit

import (
	"fmt"
	"strings"
	"testing"
)

func TestCompactUnifiedDiff(t *testing.T) {
	t.Run("keeps a small diff unchanged", func(t *testing.T) {
		diff := "diff --git a/a.go b/a.go\n+a\n"
		if got := compactUnifiedDiff(diff, len(diff)); got != diff {
			t.Fatalf("compactUnifiedDiff() = %q, want original diff", got)
		}
	})

	t.Run("shares the limit across files", func(t *testing.T) {
		var diff strings.Builder
		for index := range 4 {
			name := fmt.Sprintf("file%d.go", index)
			fmt.Fprintf(&diff, "diff --git a/%s b/%s\n--- a/%s\n+++ b/%s\n@@ -1 +1 @@\n", name, name, name, name)
			for range 100 {
				diff.WriteString("+a reasonably long changed line\n")
			}
		}

		const limit = 800
		got := compactUnifiedDiff(diff.String(), limit)
		if len(got) > limit {
			t.Fatalf("len(compactUnifiedDiff()) = %d, want at most %d", len(got), limit)
		}
		for index := range 4 {
			header := fmt.Sprintf("diff --git a/file%d.go b/file%d.go", index, index)
			if !strings.Contains(got, header) {
				t.Errorf("compactUnifiedDiff() omitted %q", header)
			}
		}
		if count := strings.Count(got, diffTruncatedMarker); count != 4 {
			t.Errorf("truncation marker count = %d, want 4", count)
		}
	})

	t.Run("gives unused bytes to larger files", func(t *testing.T) {
		small := "diff --git a/small b/small\n+x\n"
		large := "diff --git a/large b/large\n" + strings.Repeat("+changed line\n", 100)
		const limit = 300
		got := compactUnifiedDiff(small+large, limit)
		if !strings.HasPrefix(got, small) {
			t.Fatalf("compactUnifiedDiff() did not preserve the small section: %q", got)
		}
		if len(got) <= limit/2 {
			t.Fatalf("len(compactUnifiedDiff()) = %d, want unused space redistributed", len(got))
		}
	})

	t.Run("handles zero and tiny limits", func(t *testing.T) {
		diff := "diff --git a/a b/a\n+changed\n"
		if got := compactUnifiedDiff(diff, 0); got != "" {
			t.Fatalf("compactUnifiedDiff(_, 0) = %q, want empty", got)
		}
		if got := compactUnifiedDiff(diff, 1); got != "d" {
			t.Fatalf("compactUnifiedDiff(_, 1) = %q, want %q", got, "d")
		}
	})
}
