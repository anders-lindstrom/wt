export default {
  "schema": 1,
  "schemaVersion": "1.5.0",
  "command": "up",
  "trunk": "main",
  "trunkSource": "config",
  "trunkRef": "origin/main",
  "onto": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  "fetched": true,
  "trunkSync": {
    "local": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    "remote": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    "localAhead": 0,
    "remoteAhead": 0,
    "fastForwarded": false,
    "skippedReason": null
  },
  "outcome": "done",
  "error": null,
  "worktrees": [
    {
      "work": "login",
      "branch": "feat_wt/login",
      "path": "/repos/app_wt/feat_wt/login",
      "result": "rebased",
      "reason": null,
      "before": "1111111aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
      "after": "2222222bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
      "failedSteps": [],
      "recovery": null,
      "pushCommand": [
        "git",
        "-C",
        "/repos/app_wt/feat_wt/login",
        "push",
        "--force-with-lease",
        "--force-if-includes",
        "origin",
        "refs/heads/feat_wt/login:refs/heads/feat_wt/login"
      ],
      "pushed": false,
      "noPushReason": null,
      "fixCommand": null,
      "ownRemoteSync": {
        "ref": "origin/feat_wt/login",
        "state": "inSync",
        "local": "1111111aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
        "remote": "1111111aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
        "localAhead": 0,
        "remoteAhead": 0,
        "fastForwarded": false,
        "allowed": false,
        "skippedReason": null
      }
    }
  ],
  "recovery": null
}
