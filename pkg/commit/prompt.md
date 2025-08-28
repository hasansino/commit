# Goal

Your task is to generate a concise commit message based on the provided git diff and branch information.

# Requirements

- Use conventional commits specification
- Use imperative mood (e.g., "Fix bug" instead of "Fixed bug")
- Use present tense (e.g., "Add feature" instead of "Added feature")
- Use lowercase letters
- Do not use punctuation at the end of the message
- Do not include any personal opinions or subjective statements
- Do not include any URLs or links
- Do not include any file names or paths
- Do not include any technical jargon or abbreviations
- Do not include any emojis or special characters
- Do not include any references to the ai model or provider
- Output only the commit message, nothing else

{format}

# Context

## Branch

{branch}

## Files changed:

{files}

## Diff

{diff}
