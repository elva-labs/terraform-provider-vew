# Contributing

Contributions are welcome through pull requests. Fork the repository, create
a branch in your fork, and open a PR against `main` for stable development or
`beta` for changes intended for the beta release. Describe the problem, the
change, and how you checked it.

Before opening a PR, run:

```shell
make fmt
make test
make docs-check
```

When changing a provider schema or documentation template, run `make docs`
and include the generated pages. See the [README](README.md) for the Go and
Terraform versions and local development setup. Live acceptance tests need
explicit credentials and a disposable project; ordinary PR checks use local
fakes and do not contact VEW.

Elva organization members maintain this repository. Maintainers review and
merge PRs. Changes to `main` and `beta` require a PR, one approving review
covering the latest push, resolved review conversations, and successful
`docs` and `tests` checks against the current target branch. New commits
dismiss earlier approvals. Direct pushes, force pushes, and branch deletion
are blocked by the repository ruleset, including for administrators.

External contributors need maintainer approval before PR workflows can run,
even if they have contributed before. A maintainer reviews the proposed code
and workflow changes before selecting **Approve workflows to run**. That
approval allows CI to run; the PR still needs a code review before merging.
Manual workflow runs and releases require repository write access.

Report vulnerabilities through the private channel in [SECURITY.md](SECURITY.md).
