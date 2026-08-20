# How to work on this repository

This repository wraps the Files.com provider for Terraform and publishes it as a Pulumi package. The
wrapper is small. The `make tfgen` target generates the schema, and `make sdk` generates the four
SDKs. The hand-owned code lives in `provider/`, and the two registry pages live in `docs/`.

## The tools

Install these versions. The `.tool-versions` file lists every one of them. The workflows pin most
of the same values in their setup steps. A few come from other sources, so raise a version here and
in the workflows together.

| Tool | Version |
| --- | --- |
| Go | 1.26.4 |
| Pulumi | 3.258.0 |
| golangci-lint | 2.12.2 |
| Node.js | 24 |
| Python | 3.11 |
| .NET | 8.0 |
| jq | 1.7.1 |

## The first build

1. Clone the repository.
2. Run `make upstream` to check out the pinned upstream submodule.
3. Run `make provider` to build the provider binary into `bin/`.

Run `make help` for every target this repository declares.

## The generated files

Do not edit `provider/cmd/pulumi-resource-filescom/schema.json` by hand. The `make tfgen` target
writes that file, and the next run overwrites every hand edit.

To change the schema, take these steps.

1. Edit `provider/resources.go`.
2. Run `make tfgen`.
3. Commit both files.

A committed schema that a fresh run does not reproduce is a defect. The
`TestCommittedSchemaMatchesAFreshGeneration` test reports it.

To repair a defect in an upstream documentation page, add one `DocRules.EditRules` entry that names
that page. Never edit the generated output.

## The checks

Run every check with one command:

```bash
make ensure
```

It runs `make tidy`, then `make lint`, then `make test_provider`. The provider tests need no network.

Two more checks run separately.

```bash
make lint_prose
```

```bash
./scripts/check-make-targets.sh
```

## The live tests

The example programs create real objects in a real Files.com account, and they delete them again.
They read the API key from the `FILES_API_KEY` environment variable.

**Warning:** If you point `FILES_API_KEY` at an account that holds real data, a failed test can leave
objects behind. Use an empty account.

Install the SDKs first. Each example program builds against the SDK for its own language.

```bash
make install_sdks
```

Compile the example programs. This step needs no API key, and it deploys nothing.

```bash
make compile_examples
```

Run the programs against the account.

```bash
make test_examples
```

Both targets take one language at a time. The `LANGUAGE` variable selects it for
`compile_examples`. The `TESTTAGS` variable selects it for `test_examples`.

| Language | Value |
| --- | --- |
| Node.js | `nodejs` |
| Python | `python` |
| .NET | `dotnet` |
| Go | `go` |

```bash
make compile_examples LANGUAGE=nodejs
```

```bash
make test_examples TESTTAGS=nodejs
```

Two more tests record a limitation that this repository cannot fix. They fail on purpose, so
`make test_examples` leaves them out. Run the target below to see whether the limitation still holds.
One test needs two users on the account. It skips and names that precondition when the account
holds one user.

```bash
make test_knownissue
```

## The pull request

1. Write the failing test first.
2. Run it.
3. Read the failure.
4. Write the code that makes it pass.
5. Run `make ensure`.
6. Run `make lint_prose`.
7. Commit the generated files that your change regenerates.
8. Open the pull request.
