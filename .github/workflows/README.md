# Workflows

These 6 workflows are hand-written. No generator owns them. If you change a workflow, run
`./.github/workflows/check-workflows.sh` before you commit.

## What starts each workflow

| Workflow | Trigger | Jobs |
| --- | --- | --- |
| `pull-request.yml` | A pull request that touches a file other than `CHANGELOG.md`, a new label on one, or a manual run | `prerequisites`, `build_sdks`, `compile_examples`, `examples`, `test_examples`, `test_examples_live`, `lint`, `unit`, `sentinel` |
| `main.yml` | A push to `main` that touches a file other than `CHANGELOG.md`, or a manual run | The pull request jobs plus `snapshot` |
| `release.yml` | A push of a `v*.*.*` tag | `prerequisites`, `build_sdks`, `unit`, `confirm_sdks`, `publish_provider`, `publish_sdks`, `publish_go_sdk` |
| `upstream-drift.yml` | The schedule `17 6 * * 1`, or a manual run | `upstream_update` |
| `pull-request-commands.yml` | A new pull request | `note` |
| `acceptance-tests.yml` | A comment on a pull request | `authorize`, `acceptance_tests` |

A tag push never starts `main.yml`. The `tags-ignore` filter drops the `v*` release tags and the
`sdk/**` tags that the `publish_go_sdk` job pushes.

## Upstream pins

The upstream provider version appears in 3 places:

1. The `upstream` submodule gitlink.
2. The `github.com/Files-com/terraform-provider-files` require line in `provider/go.mod`.
3. The same require line in `provider/shim/go.mod`.

The `upstream_update` job runs `./scripts/upstream.sh check` first. That command fails when the 3
pins hold different versions. The job then compares the gitlink tag with the latest upstream release.
When the 2 match, the job stops.

When the pin is behind, the job moves it. Upstream cuts a release most days, so a check that demands
the latest tag would fail most weeks. The job runs these steps instead:

1. Check out the latest tag inside `upstream`.
2. Set the same version in `provider/go.mod` and `provider/shim/go.mod`, then run `go mod tidy` in
   both.
3. Commit the 3 pins.
4. Run `make tfgen`, commit the schema, then run the worktree check.
5. Generate the 4 SDKs, commit them, then run the worktree check.
6. Push the `bot/upstream-update` branch and open a pull request for it.

The worktree check after each commit fails when the generation changed a file outside `provider/`
or `sdk/`. Such a file would otherwise drop out of the pull request unseen.

The pull request body links the upstream compare view and says whether the schema changed. Most
upstream releases change only the version of the Files.com SDK, and the schema stays the same. A
later run with the same branch open rewrites the branch and the body. GitHub starts no checks on a
pull request that `GITHUB_TOKEN` opens. Close it and open it again to start them.

## Job chain

1. The `prerequisites` job generates the schema and builds the provider. Then it uploads
   `provider.tar.gz`.
2. The `build_sdks` job downloads that artifact, asks the binary for its schema, then generates
   and builds one SDK per language.
3. The `compile_examples` job compiles one example program per language against the built SDK.
4. The `examples` job runs the tests in `examples/` that read no API key. It needs no build.
5. The `test_examples` job deploys the example programs from the recorded cassettes. It reads no
   API key.
6. The `test_examples_live` job deploys the same programs against the account. This job spends
   money, so a gate guards it.
7. The `lint` job runs 4 checks. See the section below.
8. The `unit` job runs `make test_provider`.
9. The `sentinel` job fails when a job above it failed. Make it the required check.

Every generation job carries 2 steps that check the worktree is clean. Each one runs
`./scripts/check-worktree-clean.sh`, which is the only copy of that comparison. A generated file
that is not committed fails the run. Keep both checks.

In `release.yml` the `confirm_sdks` job reads the 4 SDK artifacts before `publish_provider` runs.
A binary on the release page that no SDK can be published against is what that order prevents.

## API key gate

The `test_examples_live` job needs a Files.com API key, so 3 conditions guard it:

- The job runs only when the pull request comes from this repository, not from a fork.
- The job runs only when the pull request carries the `run-live-tests` label.
- The test step runs only when `FILES_API_KEY` carries a value.

The `test_examples` job replays the recorded cassettes, so it reads no API key and no gate holds it
back. It runs on every pull request, a fork pull request included. A missing cassette fails the job
and names the file, so an unrecorded test cannot pass unseen.

A fork pull request still gets every check that spends no money.

## Record the fixtures

The `test_examples` job reads cassettes that a person records by hand. A record run calls
Files.com, so it needs the API key and it spends the account.

**Warning:** If you record against an account that holds real data, a failed run can leave objects
behind. Use an empty account.

1. Run `make test_examples` to replay the committed cassettes.
2. Export the API key: `export FILES_API_KEY=your-api-key`.
3. Run `make record_examples` to write one cassette for each test.
4. Run `make test_examples_live` to run the same tests against the account.

A record run writes one cassette and one seed file for each test, under
`examples/testdata/cassettes/`. Commit both files. The scrub drops the API key header before a
cassette reaches the disk, and a test reads every committed cassette back for a key.

## Acceptance-test command

A fork pull request never gets the API key from `pull-request.yml`. A maintainer starts the
acceptance tests with a comment instead.

1. The `pull-request-commands.yml` workflow posts the command list on a new pull request. It
   checks nothing out, so no code from the branch runs there.
2. A maintainer reads the changes.
3. The maintainer comments `/run-acceptance-tests` on the pull request.
4. The `authorize` job reads `author_association`. Only `OWNER`, `MEMBER`, and `COLLABORATOR`
   pass. The job then reads the head commit of the pull request and reports it in a comment.
5. The `acceptance_tests` job checks out that commit by its SHA and runs `make test_examples_live`.

**Warning:** The command runs the code of the pull request with the API key of this repository.
Read the changes before you comment.

The `acceptance_tests` job fails when `FILES_API_KEY` is empty. A maintainer asked for the run, so
a suite that skipped itself for a missing key would report a pass nobody earned.

## Secrets

| Secret | Consumed by | Purpose |
| --- | --- | --- |
| `GITHUB_TOKEN` | Every workflow | The version action, the release upload, the upstream release lookup, and the upstream pull request |
| `FILES_API_KEY` | The `test_examples_live` job in `pull-request.yml` and `main.yml`, and the `acceptance_tests` job in `acceptance-tests.yml` | The live example deployments |
| `NPM_TOKEN` | The `publish_sdks` job in `release.yml` | The npm publish |
| `NUGET_PUBLISH_KEY` | The `publish_sdks` job in `release.yml` | The NuGet push |

PyPI needs no secret. The `publish_sdks` job publishes with Trusted Publishing, so it asks for the
`id-token: write` permission instead. Configure the publisher on PyPI before the first release.

## Publish rules

Only `release.yml` publishes, and only a `v*.*.*` tag push starts it. The npm tag follows the
version: a version with a prerelease part publishes under `dev`, and any other version publishes
under `latest`.

## What the lint job runs

The `lint` job runs 5 checks, in this order.

1. `./.github/workflows/check-workflows.sh` asserts the rules of this directory.
2. That script runs `actionlint` over every workflow file.
3. `./scripts/check-make-targets.sh` asserts the Makefile vocabulary.
4. `./scripts/check-internal-references.sh` reads every tracked file for a reference to a
   private document.
5. `make lint` reads the Go code, and `make lint_prose` reads the shipped prose.

The job installs `actionlint` from its GitHub release page and checks the archive against the
`ACTIONLINT_SHA256` digest that the workflow pins. When `actionlint` is absent and `CI` is `true`,
the check script fails instead of skipping.

## Pinned versions

Every action carries an exact version. The Pulumi CLI, `pulumictl`, and `golangci-lint` install from
a pinned release inside a `run` step. The `ACTIONLINT_VERSION` and `ACTIONLINT_SHA256` variables pin
`actionlint`.

| Tool | Version |
| --- | --- |
| Pulumi CLI | 3.258.0 |
| `pulumictl` | v0.0.50 |
| `golangci-lint` | v2.12.2 |
| `actionlint` | 1.7.12 |
| Node | 24 |
| Yarn | 1.22.22 |
| Python | 3.11 |
| .NET SDK | 8.0.x |
| Go | The version `provider/go.mod` names |

## What the check script asserts

The `lint` job runs `check-workflows.sh`. The script asserts that:

- The 4 named workflow files exist, and the script reads every `.yml` and `.yaml` file in this
  directory.
- Every `uses:` line names an action on the pinned list.
- No workflow reaches into infrastructure that belongs to the Pulumi organization.
- The `release.yml` triggers hold one entry: the `v*.*.*` tag push.
- The `main.yml` triggers ignore the `v*` and `sdk/**` tags.
- No publish command runs outside `release.yml`.
- The `test_examples_live` job carries the fork gate and reads the API key.
- That job waits for the `run-live-tests` label in `pull-request.yml`.
- The `test_examples` job reads no secret and carries no gate.
- Each of the 2 jobs runs its own make target, and `acceptance-tests.yml` runs the live one.
- The generation jobs check the worktree twice, and each check runs the shared script.
- No workflow compares the worktree with its own copy of the command.
- No `pull_request_target` workflow checks a branch out.
- Every secret reference names one of the 4 secrets in the previous table.

The script reads the lines YAML reads. A step behind a `#` counts as absent.

## Makefile contract

The workflows call `make` for every build step. Exactly 3 calls pass a variable, where `<language>`
is `nodejs`, `python`, `dotnet`, or `go`.

| Call | Job |
| --- | --- |
| `make compile_examples LANGUAGE=<language>` | `compile_examples` |
| `make test_examples TESTTAGS=<language>` | `test_examples` |
| `make test_examples_live TESTTAGS=<language>` | `test_examples_live` |
