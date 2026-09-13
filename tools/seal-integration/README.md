# Seal CLI integration

This regression runs actual `seal` and `evalctl` executables in a private,
disposable Git repository with an explicit temporary Eval state root. It creates
ordinary Tasks and passing/failing Runs through Seal's CLI, then exercises partial
export, persisted collection, same-digest Completion, conflicting digest replacement,
and repeated collection. Receipts must confirm durability, and fact/report output
must exclude the original repository path and Task/Run identities. No installed
binary, real Journal, or user configuration is changed.

The integer boundary cases are deliberately fabricated historical-format Evidence:
copies of the real failed fixture have their saved exit codes replaced with
`9223372036854775808` and `-9223372036854775809`, with recomputed manifests. Seal's
real `run show` and export must validate these fixtures before Eval consumes them.
They prove compatibility and exact storage/reopen behavior, not that an operating
system process returned those exit statuses.

CI builds Seal from the reviewed support commit
`11f6a304064be475fec75fe815bdcce85ae8a973` in `jgoneit/seal`, and Eval from the PR
checkout. Seal's unchanged `0.3.0-rc.4` version string alone does not identify export
support. The integration job uses Go 1.26.6 because that pinned Seal source requires
it. This contract test does not prove installation, Host lifecycle coverage, or
human-reviewed task quality.

To run locally, build both repositories to disposable output paths and pass their
absolute binary paths:

```sh
python3 tools/seal-integration/run.py --seal /absolute/temp/seal --evalctl /absolute/temp/evalctl
```

Python 3.10+ and Git are required; the script uses only Python's standard library.
