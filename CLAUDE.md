# CLAUDE.md

Style for Go code in this repository. Where it leaves a choice, match the
code around the change; the project's own contributor docs take precedence.

## Go style

Follow [Effective Go](https://go.dev/doc/effective_go),
[Go Code Review Comments](https://go.dev/wiki/CodeReviewComments) and the
[Google Go style guide](https://google.github.io/styleguide/go/).

### Naming

- Names say what a thing is or does in the fewest words that stay clear.
  Short for small scopes (`i`, `r`, `ctx`), descriptive for wide ones.
- Don't repeat the package or receiver in a name: `user.List`, not
  `user.ListUsers`; `Order.ID`, not `Order.OrderID`.
- Getters have no `Get` prefix: `Owner()`, not `GetOwner()`.
- Functions returning a value are named for that value, as nouns: `Users()`,
  `ActiveUsers()`. An adjective alone (`Active()`) reads as a boolean.
- Name a lookup for the thing it returns, in the word the code already uses
  for it, and say what it is found by when the parameters don't:
  `InstanceAt(network, ip)` returning an instance's ID, not
  `Holder(network, ip)`, which coins a second word for an instance. Search
  for a name before taking it: one already meaning something else is wrong
  however well it reads.
- Booleans and predicates read as a question: `isEmpty`, `hasPrefix`,
  `sameKeys`, `validName`, `Matches`, `Expired`.
- Functions that act are verbs, with the object when it isn't obvious:
  `removeExpired`, `loadConfig`. Variants qualify the verb rather than
  inventing a new one: `tryClose`, `deleteUnlessLocked`.
- Initialisms keep one case: `ID`, `URL`, `HTTP`, `JSON`; `userID`, not
  `userId`.
- No abbreviations beyond Go's common ones (`ctx`, `err`, `id`, `ref`, `cfg`,
  `req`, `resp`, `buf`). Spell the rest out.
- One word per concept across the codebase. Once something has a name, every
  identifier, log message, metric and doc page uses it; don't mix in
  synonyms.
- Use a concept's full name in every identifier, not a shortened form of
  it, however clear the short one seems where it stands. A type
  `allocationTable` goes with `newAllocationTable`, `allocationTableOf`,
  `allocationTables` and `allocationTableExt`, not `table`, `tableOf`,
  `tables` and `tableExt`: a bare word such as table already means something
  else somewhere (an iptables table, a routing table, an output format).
- Single-method interfaces are named for the method plus `-er` (`Reader`,
  `Notifier`). Define an interface where it is consumed, not where it is
  implemented.
- Errors: variables `ErrX`, types `XError`.
- Files are named for what they hold, in the words of the concept, lowercase,
  with words separated by underscores: `instance_template.go`, not
  `template.go` (too vague) or `instancetemplate.go` (run together). Its test
  file is the same name with `_test`, `instance_template_test.go`, and a
  build-constrained one with its suffix, `instance_template_linux.go`.

### Structure

- Accept interfaces, return concrete types.
- Handle an error once: return it wrapped with context
  (`fmt.Errorf("load config: %w", err)`), or log it, not both.
- Match errors with `errors.Is` and `errors.As`, never by their message.
- Early returns over nested `else`; `switch` over long `if`/`else if` chains.
- Make the zero value useful where it can be.
- Don't add a helper that only wraps a few lines used twice; inline it.
  Extract one when it names an idea.
- Guard shared state with a mutex named `mu`, placed above the fields it
  guards, and never hold it across I/O.
- `context.Context` is the first parameter and is never stored in a struct.

### Comments

Follow [Go Doc Comments](https://go.dev/doc/comment).

- Be concise. One or two sentences is usually enough; cut any word that
  doesn't add meaning.
- Every exported identifier has a doc comment, and unexported ones do when
  their purpose isn't obvious. A doc comment is a full sentence starting with
  the name: `// Close releases the connection.`
- Say what a thing does and why, not how. Don't restate the signature, the
  type, or what the code plainly says.
- Inside a function, comment only what isn't obvious: a decision, an
  invariant, a workaround, the reason for an order. Don't narrate each step.
- Document behaviour a caller depends on: what's returned on failure or when
  nothing is found, whether it's safe for concurrent use, what a zero or nil
  argument means.
- The package comment goes in one place (`doc.go` for larger packages) and
  starts `// Package name ...`.
- No commented-out code, banner or divider comments, author names, or change
  history; version control has those.
- Track follow-up work where the project already does, not in scattered
  `TODO` comments.
- Keep comments true: a change that makes one wrong updates it.
- Wrap at about 80 columns, like the surrounding code.

### Messages

- Use one spelling convention (US or British) throughout, as the project
  already does, in comments too.
- Error strings and log messages are lowercase with no trailing punctuation.
- With structured logging, values go in attributes with consistent keys
  (`slog.String("user_id", id)`), not interpolated into the message.
- An error a user will see says what to do about it, when there is
  something to do.

### Tests

- Table-driven where cases share a shape, with `t.Run` naming each case.
- Test names describe behaviour (`TestExpiredTokenIsRefreshed`), with a doc
  comment when the name doesn't say enough.
- Prefer small hand-written fakes of the interface a package consumes over
  mocking frameworks.
- `t.Helper()` in helpers; `t.Fatal` only when the test cannot go on.
- Run with `-race`.

## Keeping things in step

- Generated code and docs are regenerated from their source, never edited by
  hand.
- A change in behaviour updates the hand-written docs that describe it.

## Before finishing

Format, lint and test, with the project's own targets where it has them
(for example `make fmt lint test`), otherwise:

```console
$ gofmt -l .
$ go vet ./...
$ golangci-lint run
$ go test -race ./...
```

Don't silence a linter without a reason: `//nolint:<linter> // why`.
