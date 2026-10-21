# Draft PR validation

Pull requests do not trigger Actions. Keep the PR in Draft while validating it.
Dispatch all three workflows against the PR head branch and exact head SHA:

```sh
pr=123
repo=cfgxy/multica-cn
head_ref=$(gh pr view "$pr" --repo "$repo" --json headRefName --jq .headRefName)
head_sha=$(gh pr view "$pr" --repo "$repo" --json headRefOid --jq .headRefOid)
gh workflow run ci.yml --repo "$repo" --ref "$head_ref" -f pr_number="$pr" -f expected_sha="$head_sha"
gh workflow run mobile-verify.yml --repo "$repo" --ref "$head_ref" -f pr_number="$pr" -f expected_sha="$head_sha"
gh workflow run mcp-build.yml --repo "$repo" --ref "$head_ref" -f pr_number="$pr" -f expected_sha="$head_sha"
```

Each workflow rejects a run unless the selected ref resolves to the supplied
SHA and that SHA is still the open PR head targeting `main`. CI runs its full
frontend and backend validation regardless of changed paths; Mobile Verify
runs lint, typecheck, and tests; MCP Server Build runs its tests, typecheck,
lint, package build, and artifact checksum. CI also runs the image-budget gate
when the PR changes image files. Review the runs and their check suites on that
exact SHA. Before marking Ready, confirm the PR head SHA has not changed since
all three runs passed; a new push requires dispatching all three workflows
again.

The aggregate CI check names are `frontend` and `backend`; the mobile check is
`mobile`. These names remain attached to the dispatched commit and are usable
as required checks. As of this change, `main` has neither classic branch
protection nor repository rulesets configured, so GitHub does not enforce any
required checks. The workflow cannot prevent a user with permission from
marking a PR Ready without dispatching or waiting for validation, nor can it
atomically prevent a head update between the final SHA check and that action.
