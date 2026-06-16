Verify a registered skill by running automated structural checks against its SKILL.md file.

Checks performed:
1. Skill exists in the registry
2. SKILL.md file is readable
3. YAML frontmatter is valid and parseable
4. Required fields are present: name and description must be non-empty
5. Body content is non-empty and substantive (> 50 characters)

Output format:
- VERDICT: PASS when all checks pass
- VERDICT: FAIL when no checks pass (e.g., file unreadable or frontmatter invalid)
- VERDICT: PARTIAL when some checks pass but others fail

Named anti-patterns that cause FAIL or PARTIAL verdicts:

1. "verification_avoidance" — The skill instructs the model to read code files
   instead of running the application. Reading code is NOT verification; you must
   run the app/tests to confirm behavior. Bodies that contain read/read_file
   instructions without corresponding run/test/execute steps trigger this
   anti-pattern.

2. "first_80_seduction" — The skill declares success or completeness after only
   validating the happy path (the first 80%). Every skill MUST describe how to
   verify edge cases, error paths, and failure modes, not just the primary
   success scenario. Bodies containing completion/done/fixed language without
   edge-case coverage trigger this anti-pattern.

Evidence requirement:
Every PASS verdict MUST include a "Command run:" block showing the actual command
executed and its output. Claims about code behavior without a matching command
execution and output are not accepted.

Use this after creating or updating a skill to confirm it is structurally sound before relying on it.
