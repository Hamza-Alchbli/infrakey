# TODO

## Remaining Risk Focus

- Compose parser edge cases.
  - The parser in `internal/compose` is custom and line-based, not a full YAML AST parser.
  - Unusual compose syntax can still be missed and should be covered with more fixtures/tests.
