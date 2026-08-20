# Registry pull request

This page is the draft of the pull request that lists this provider on the Pulumi Registry. Nobody
opened it. The registry lists a package only after a release exists, and the release is a human
decision.

The facts on this page match `pulumi/registry` on 19 August 2026. Its default branch is `master`.

## What the pull request changes

The pull request changes two files and nothing else. The registry generates the package metadata
and the documentation after the merge, so the pull request commits no generated file. A pull
request that touches a third file is rejected by the automation before any other check runs.

### 1. The package entry

`community-packages/package-list.json` holds one object with an `include` array. Append one object
to the end of that array:

```json
{
  "repoSlug": "jschady/pulumi-filescom",
  "schemaFile": "provider/cmd/pulumi-resource-filescom/schema.json"
}
```

An entry carries these two keys and no other. The array is not sorted, and the merged entries sit
in the order they arrived, so append rather than insert.

### 2. The publisher name

`tools/resourcedocsgen/pkg/publishers/publisher-names.json` maps the `publisher` property of a
schema to the slug the registry puts in the package URL. This schema sets `publisher` to `jschady`,
and that key is absent from the map today. Add one pair, in alphabetical position:

```json
"jschady": "jschady"
```

Without the pair the registry cannot build the package page. This is the one file the automation
accepts beside the package list, because a new publisher has to arrive with its own entry.

## Checklist before you open it

Each row is a decision a person makes. Work through the table from the top.

| Step | What you do | Why it blocks |
| --- | --- | --- |
| Approve the mark | Look at `docs/logo.svg` and keep it or replace it | The registry page and the NuGet package both show it |
| Decide the code of conduct | Ship without one, or give a contact address | The address belongs to the owner of this repository |
| Re-check the package names | Run `./scripts/check-package-names.sh` | Nothing reserves a name without publishing it |
| Squash the history | Follow the steps under "How to squash the history" | The published history carries one commit |
| Create the live-test label | Add a label named `run-live-tests` to the repository | The label condition in pull-request.yml reads it |
| Decide the manual runs | Keep or remove `workflow_dispatch` on the jobs that read the API key | A maintainer can start a billed run by hand |
| Publish the release | Tag `vX.Y.Z` and let the release workflow run | The automation reads the latest published release |
| Install the plugin | Run the command in the next section | This is the first real test of the download URL |

### How to squash the history

The published history carries one commit. Run these steps last, after every other check passes
and before the push. The commit keeps the tree exactly as it is now.

1. Confirm the tree is clean with `git status`.
2. Record the current commit with `git rev-parse HEAD`.
3. Start an orphan branch that keeps the index: `git checkout --orphan release`.
4. Write the one commit with `git commit -m "Initial release"`.
5. Confirm the submodule pin survived with `git ls-tree HEAD upstream`.
6. Compare the trees with `git diff <recorded-commit> HEAD`. The command prints nothing.
7. Move the branch into place with `git branch -M main`.
8. Tag the new commit with `git tag vX.Y.Z`.
9. Push the branch and the tag.

To replace the mark, write the new artwork to `docs/logo.svg` and a 256 by 256 pixel copy to
`docs/logo.png`. Then run `make generate_dotnet`, which copies the second file into the .NET SDK.
NuGet reads the leading bytes of a package icon and rejects an SVG, so the two formats both ship.

Check the download URL after the release publishes:

```bash
pulumi plugin install resource filescom 0.1.0 --server github://api.github.com/jschady/pulumi-filescom
```

## Pull request body

> ### Add the Files.com provider
>
> This adds `jschady/pulumi-filescom`, a statically bridged provider. It wraps the
> [Files.com provider for Terraform](https://github.com/Files-com/terraform-provider-files),
> pinned at `v0.1.882`.
>
> The provider carries 60 resources and 80 data sources. This repository is Apache 2.0, and the
> upstream MIT license stays with the upstream code in `LICENSE-upstream`.
>
> `jschady` is a new publisher, so this pull request also adds the display name.

## `/check` command

The automation reads the live provider repository, not the diff in the pull request. To fix a red
check, change this repository and comment `/check` on the pull request. Do not push a new commit to
the registry fork.

1. Read the fact sheet the bot posts on the pull request.
2. Fix what is red here. One of these repairs it:
   - cut a release
   - publish an SDK
   - correct the schema path
3. Comment `/check` on the pull request.
4. Wait for the bot to rewrite its fact sheet.

The command runs at most once every 10 minutes. The author of the pull request can run it, and so
can a maintainer of `pulumi/registry`. A maintainer review is still required to merge.

## What the automation reads

| Check | Blocking | What it reads |
| --- | --- | --- |
| Changed files | Yes | Only the two files above |
| Published release | Yes | The latest release of `jschady/pulumi-filescom` |
| Schema path | Yes | `schemaFile` at that release |
| Documentation build | Yes | `resourcedocsgen metadata from-github` |
| Registry page | Yes | `docs/_index.md` in this repository |
| Plugin install | Yes | `pulumi plugin install resource filescom <tag>` |
| SDK install | No | The npm, PyPI and Go packages the schema advertises |
| Documentation lint | No | Relative images and links in `docs/_index.md` |
| Publisher entry | No | The `publisher` property against the map |

The version comes from the release tag. The committed schema carries no version, and the
automation never reads one from it.

## Notes

### Empty descriptions

The registry shows 312 properties with no description. Upstream declares 310 of them with no text,
and the code generator adds the other 2. The `docs/_index.md` page states the counts. This
repository invents no replacement text, so closing the gap needs an upstream change.

The issue that would close the gap does not exist yet. Two steps open it.

1. File one issue on
   [Files-com/terraform-provider-files](https://github.com/Files-com/terraform-provider-files/issues)
   that asks for a description on each property below.
2. Record the issue link here.

| Where | Property |
| --- | --- |
| `files_bundle` | `preview_only` |
| `files_lock` | `depth` |
| `files_lock` | `scope` |
| `files_lock` | `type` |
| The provider configuration | `environment` |
| The provider configuration | `feature_flags` |

The data source outputs repeat the same 4 resource properties, and add
`files_file_migration.files_total` and `files_sso_strategy.provision_attachments_permission`.

The table names the 6 top-level properties. The other 298 are nested properties under
`files_automation.definition` and `files_holiday_calendar.definition`, and one issue covers them all.

### Code samples in the Terraform language

The schema carries 140 example blocks. Each one holds the same example in 7 languages, and one of
those languages is the Terraform language. The registry renders it under a tab named HCL. That tab
left preview status on 12 August 2026, and every bridged provider on the registry ships the same
blocks. This repository keeps them.

Two facts about those blocks are worth knowing.

- Each block opens with a `pulumi` header rather than a `terraform` header. The bridge rewrites the
  word, so the sample is not valid Terraform. Every bridged provider shows the same rewrite.
- The .NET SDK repeats 240 of these blocks inside its documentation comments. The other 3 SDKs
  carry none. The generator reads the whole schema rather than the part for one language.

### Dynamic path is a different process

A dynamically bridged provider, the kind you add with
`pulumi package add terraform-provider Files-com/files`, cannot be listed by pull request. That path
needs a "New Package" issue instead. This repository is the static path, and it commits a schema, so
the pull request is correct here.
