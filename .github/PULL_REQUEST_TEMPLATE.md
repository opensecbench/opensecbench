<!--
Thanks for contributing! A few things to know:
- For a change that makes or alters a design decision, an ADR should have been proposed first
  (propose before building big — see CONTRIBUTING.md and docs/adr/adr-0057-decision-process.md).
- Keep the PR focused: one logical change is much easier to review.
-->

### Summary

What does this change do, and why?

### Related

<!-- Link the issue and/or ADR this relates to, e.g. "Closes #123", "Implements ADR-00NN". -->

### Checklist

- [ ] `go build ./...` and `go test ./...` pass locally
- [ ] Code is `gofmt`-clean and `golangci-lint run` is happy
- [ ] No secrets, credentials, real targets, or client data in the diff (fixtures included)
- [ ] Tests added/updated for the change where practical
- [ ] Docs updated if behaviour changed
- [ ] If this makes/changes a design decision: ADR added or updated, and `make adr-index` run
