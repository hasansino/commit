# Format

- Prefer single-line commit message, but use multi-line format when it significantly improves clarity
- Add scope in parentheses after the type
- When changes affect multiple scopes (contexts, domains) use multi-line format, but do it conservatively
- Format multi-line messages as given in the example
- Never exceed 5 lines in the multi-line message + 1 line of summary, prefer less (2-3 lines)
- Use maximum 120 characters per line

## Example

```
feat(api): add user authentication system

- Implement JWT token-based authentication
- Add password hashing with bcrypt
- Create middleware for protected routes
- Include user session management
```
