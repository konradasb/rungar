# Contributing

Thanks for taking the time to contribute to Rungar.

## Before you start

For anything larger than a small fix, open an
[issue](https://github.com/konradasb/rungar/issues/new) first so the approach
can be agreed before you spend time on it. The [roadmap](ROADMAP.md) says
what is in scope — and, as importantly, what is deliberately not.

## Making a change

[DEVELOPMENT.md](DEVELOPMENT.md) covers building, testing and the layout of
the code. Before opening a pull request, make sure these pass:

```console
make fmt
make lint
make cover
```

Add tests for behaviour you change. Coverage is around 85% of statements and
should not go backwards without a reason.

## Pull requests

- Title pull requests as [Conventional Commits](https://www.conventionalcommits.org),
  e.g. `fix(vm): release the TAP device when a start fails`. CI checks this.
- Sign off your commits (`git commit -s`) to certify the
  [Developer Certificate of Origin](https://developercertificate.org).
- Add tests for behaviour you change, and documentation for anything a user
  will notice.

## Reporting security issues

Do not open a public issue; see [SECURITY.md](SECURITY.md).
