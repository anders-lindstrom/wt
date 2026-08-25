Copies of the seven real worktree.conf files, used as parser and validator
conformance fixtures.

AWS_SETUP_ENABLED, REPO_NAME and WORKTREE_LAYOUT lines are stripped: all three
are retired keys that the validator now rejects by design. The repos themselves are migrated in
Plan 3; these fixtures represent the post-migration state.
