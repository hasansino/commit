package commit

import (
	"fmt"
	"strings"
)

const diffExcerptNotice = "[Diff excerpts: not a complete patch; sampled lines may be nonconsecutive.]\n"

type diffFileSection struct {
	raw, header    string
	hunks          []diffHunkSection
	added, removed int
}

type diffHunkSection struct {
	raw, location  string
	added, removed []string
}

// compactUnifiedDiff keeps complete diffs when they fit. Otherwise it shares
// space across files, hunks, and both sides of each change. Excerpts carry the
// original change counts and explicit omissions; they are not applicable patches.
func compactUnifiedDiff(diff string, maxBytes int) (string, error) {
	if maxBytes <= 0 {
		return "", fmt.Errorf("maximum diff size must be greater than zero")
	}
	diff = strings.ToValidUTF8(diff, "\uFFFD")
	if len(diff) <= maxBytes {
		return diff, nil
	}

	sections := splitUnifiedDiffSections(diff)
	files := make([]diffFileSection, len(sections))
	minimum, desired := make([]int, len(files)), make([]int, len(files))
	for i, section := range sections {
		files[i] = parseDiffFile(section)
		minimum[i] = files[i].minimumSize()
		desired[i] = len(section)
	}

	// Reserve the longest possible omission count before assigning any space.
	omission := func(count int) string { return fmt.Sprintf("[Omitted files: %d]\n", count) }
	available := maxBytes - len(diffExcerptNotice) - len(omission(len(files)))
	budgets := allocateDiffBudgets(minimum, desired, available)
	var body strings.Builder
	omitted := 0
	for i, file := range files {
		if budgets[i] == 0 {
			omitted++
			continue
		}
		body.WriteString(file.render(budgets[i]))
	}
	if omitted == len(files) {
		return "", fmt.Errorf(
			"maximum diff size of %d bytes is too small to describe staged changes; increase --max-diff-size-bytes",
			maxBytes,
		)
	}
	return diffExcerptNotice + omission(omitted) + body.String(), nil
}

func splitUnifiedDiffSections(diff string) []string {
	const separator = "\ndiff --git "
	starts := []int{0}
	for searchFrom := 0; searchFrom < len(diff); {
		marker := strings.Index(diff[searchFrom:], separator)
		if marker < 0 {
			break
		}
		start := searchFrom + marker + 1
		starts = append(starts, start)
		searchFrom = start + len("diff --git ")
	}
	sections := make([]string, 0, len(starts))
	for i, start := range starts {
		end := len(diff)
		if i+1 < len(starts) {
			end = starts[i+1]
		}
		sections = append(sections, diff[start:end])
	}
	return sections
}

func parseDiffFile(raw string) diffFileSection {
	file := diffFileSection{raw: raw, header: raw}
	offset, hunkStart := 0, 0
	var previousKind byte
	for line := range strings.SplitAfterSeq(raw, "\n") {
		if strings.HasPrefix(line, "@@ ") {
			if len(file.hunks) == 0 {
				file.header = raw[:offset]
			} else {
				file.hunks[len(file.hunks)-1].raw = raw[hunkStart:offset]
			}
			location := strings.TrimSuffix(line, "\n")
			if end := strings.Index(location[3:], " @@"); end >= 0 {
				location = location[:3+end+3] // Drop the optional function name.
			}
			file.hunks = append(file.hunks, diffHunkSection{location: location})
			hunkStart = offset
			previousKind = 0
		} else if len(file.hunks) > 0 && len(line) > 0 {
			hunk := &file.hunks[len(file.hunks)-1]
			switch line[0] {
			case '+':
				hunk.added = append(hunk.added, terminatedDiffLine(line))
				file.added++
			case '-':
				hunk.removed = append(hunk.removed, terminatedDiffLine(line))
				file.removed++
			case '\\':
				// Keep the no-newline marker attached to the line it describes.
				switch previousKind {
				case '+':
					hunk.added[len(hunk.added)-1] += terminatedDiffLine(line)
				case '-':
					hunk.removed[len(hunk.removed)-1] += terminatedDiffLine(line)
				}
			}
			previousKind = line[0]
		}
		offset += len(line)
	}
	if len(file.hunks) > 0 {
		file.hunks[len(file.hunks)-1].raw = raw[hunkStart:]
	}
	return file
}

func terminatedDiffLine(line string) string {
	if strings.HasSuffix(line, "\n") {
		return line
	}
	return line + "\n"
}

func (f diffFileSection) summary(omitted int) string {
	return fmt.Sprintf(
		"[File totals: +%d -%d lines; omitted hunks: %d/%d]\n",
		f.added,
		f.removed,
		omitted,
		len(f.hunks),
	)
}

func (f diffFileSection) minimumSize() int {
	if len(f.hunks) == 0 {
		return len(f.raw) // Binary, rename, and mode metadata must remain intact.
	}
	return min(len(f.raw), len(f.header)+len(f.summary(len(f.hunks))))
}

func (f diffFileSection) render(budget int) string {
	if len(f.raw) <= budget {
		return f.raw
	}
	minimum, desired := make([]int, len(f.hunks)), make([]int, len(f.hunks))
	for i, hunk := range f.hunks {
		minimum[i] = min(len(hunk.raw), len(hunk.heading())+len(hunk.omission(0, 0)))
		desired[i] = len(hunk.raw)
	}
	available := budget - len(f.header) - len(f.summary(len(f.hunks)))
	budgets := allocateDiffBudgets(minimum, desired, available)
	var body strings.Builder
	omitted := 0
	for i, hunk := range f.hunks {
		if budgets[i] == 0 {
			omitted++
			continue
		}
		body.WriteString(hunk.render(budgets[i]))
	}
	return f.header + f.summary(omitted) + body.String()
}

func (h diffHunkSection) heading() string {
	return "[Hunk excerpt: " + h.location + "]\n"
}

func (h diffHunkSection) omission(removed, added int) string {
	return fmt.Sprintf("[Omitted lines: %d removed, %d added; unchanged context not shown]\n",
		len(h.removed)-removed, len(h.added)-added)
}

func (h diffHunkSection) render(budget int) string {
	if len(h.raw) <= budget {
		return h.raw
	}
	available := budget - len(h.heading()) - len(h.omission(0, 0))
	sizes := []int{diffLinesSize(h.removed), diffLinesSize(h.added)}
	budgets := allocateDiffBudgets([]int{0, 0}, sizes, available)
	removed, removedCount := sampleDiffLines(h.removed, budgets[0])
	added, addedCount := sampleDiffLines(h.added, budgets[1])
	// Whole lines may leave space unused. Give either side a chance to use it.
	remaining := available - len(removed) - len(added)
	if remaining > 0 {
		removed, removedCount = sampleDiffLines(h.removed, len(removed)+remaining)
		remaining = available - len(removed) - len(added)
		added, addedCount = sampleDiffLines(h.added, len(added)+remaining)
	}
	return h.heading() + removed + added + h.omission(removedCount, addedCount)
}

func diffLinesSize(lines []string) int {
	size := 0
	for _, line := range lines {
		size += len(line)
	}
	return size
}

func sampleDiffLines(lines []string, budget int) (string, int) {
	selected := make([]bool, len(lines))
	count := 0
	for _, i := range diffSampleOrder(len(lines)) {
		if len(lines[i]) <= budget {
			selected[i] = true
			budget -= len(lines[i])
			count++
		}
	}
	var result strings.Builder
	for i, line := range lines {
		if selected[i] {
			result.WriteString(line)
		}
	}
	return result.String(), count
}

// Visit both ends, then successively split the intervening gaps. Small samples
// represent the whole change, rather than only its beginning. Rendering still
// follows source order.
func diffSampleOrder(count int) []int {
	if count == 0 {
		return nil
	}
	order := make([]int, 0, count)
	order = append(order, 0)
	if count == 1 {
		return order
	}
	order = append(order, count-1)
	type interval struct{ start, end int }
	gaps := []interval{{1, count - 1}}
	for i := 0; i < len(gaps); i++ {
		gap := gaps[i]
		if gap.start >= gap.end {
			continue
		}
		middle := gap.start + (gap.end-gap.start)/2
		order = append(order, middle)
		gaps = append(gaps, interval{gap.start, middle}, interval{middle + 1, gap.end})
	}
	return order
}

// Reserve complete summaries first, then share remaining bytes equally,
// redistributing unused allowances from small sections. When summaries alone
// exceed the limit, select sections spread across the input.
func allocateDiffBudgets(minimum, desired []int, budget int) []int {
	budgets := make([]int, len(minimum))
	if budget <= 0 {
		return budgets
	}
	var active []int
	for _, i := range diffSampleOrder(len(minimum)) {
		if minimum[i] <= budget {
			budgets[i] = minimum[i]
			budget -= minimum[i]
			if desired[i] > minimum[i] {
				active = append(active, i)
			}
		}
	}
	for budget > 0 && len(active) > 0 {
		share, remainder := budget/len(active), budget%len(active)
		next := active[:0]
		for offset, i := range active {
			allowance := share
			if offset < remainder {
				allowance++
			}
			allowance = min(allowance, desired[i]-budgets[i])
			budgets[i] += allowance
			budget -= allowance
			if budgets[i] < desired[i] {
				next = append(next, i)
			}
		}
		active = next
	}
	return budgets
}
