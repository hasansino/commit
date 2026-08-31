package commit

import "strings"

const diffTruncatedMarker = "... diff truncated ...\n"

// compactUnifiedDiff reduces a unified diff while distributing the available
// bytes across files. Small file diffs keep their full content and return their
// unused share to larger files.
func compactUnifiedDiff(diff string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(diff) <= maxBytes {
		return diff
	}

	sections := splitUnifiedDiffSections(diff)
	budgets := balancedSectionBudgets(sections, maxBytes)

	var result strings.Builder
	result.Grow(maxBytes)
	for index, section := range sections {
		result.WriteString(truncateDiffSection(section, budgets[index]))
	}
	return result.String()
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
	for index, start := range starts {
		end := len(diff)
		if index+1 < len(starts) {
			end = starts[index+1]
		}
		sections = append(sections, diff[start:end])
	}
	return sections
}

func balancedSectionBudgets(sections []string, maxBytes int) []int {
	budgets := make([]int, len(sections))
	active := make([]int, len(sections))
	for index := range sections {
		active[index] = index
	}

	remaining := maxBytes
	for len(active) > 0 {
		share := remaining / len(active)
		large := active[:0]
		for _, index := range active {
			if len(sections[index]) <= share {
				budgets[index] = len(sections[index])
				remaining -= len(sections[index])
				continue
			}
			large = append(large, index)
		}
		if len(large) < len(active) {
			active = large
			continue
		}

		for offset, index := range active {
			budgets[index] = share
			if offset < remaining%len(active) {
				budgets[index]++
			}
		}
		break
	}
	return budgets
}

func truncateDiffSection(section string, budget int) string {
	if budget <= 0 {
		return ""
	}
	if len(section) <= budget {
		return section
	}
	if budget <= len(diffTruncatedMarker) {
		return section[:budget]
	}

	contentBudget := budget - len(diffTruncatedMarker)
	return section[:contentBudget] + diffTruncatedMarker
}
