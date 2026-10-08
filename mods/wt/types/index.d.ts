/** A branch against the ref a push of it would replace, as `wt status --json` reports it. */
export type OwnRemote = {
  ref: string | null
  /** none | gone | unknown | inSync | ahead | behind | rebased | diverged */
  state: string
  ahead: number | null
  behind: number | null
  /** null | diverged | operation | dirty | session: why a run would refuse. */
  blocks: string | null
}

export type WtSession = { name: string; kind: string; state: string }

/** The worktree the session stands in, digested from `wt status --json`. */
export type Plan = {
  work: string
  branch: string | null
  path: string
  isMain: boolean
  trunk: string
  trunkRef: string
  /** When the trunk ref last moved here (ISO), or null: status does not fetch. */
  trunkFetchedAt: string | null
  /** The repository's git directory, absolute; null when it could not be read. */
  gitDir: string | null
  /** When the repository last fetched anything (ms since the epoch), or null. */
  fetchedAt: number | null
  /** Commits origin's trunk has that local trunk lacks. */
  trunkRemoteAhead: number
  /** null when wt could not count them. */
  behind: number | null
  ahead: number | null
  /** clean | dirty | unreadable */
  tree: string
  /** null from a wt older than status 1.6.0. */
  ownRemote: OwnRemote | null
  upEligible: boolean
  upIneligibleCode: string | null
  upIneligibleReason: string | null
  /** Passed to `wt up --expect`. */
  token: string | null
  /** The other worktrees a run on this one would move, parents first. */
  stack: string[]
  /** Other agent sessions in the worktree. */
  sessions: WtSession[]
  /** Why the sessions could not be listed, when they could not. */
  sessionsError: string | null
  schemaVersion: string
}

/** What the last action from the dialog or `/wt` came to. */
export type Outcome = {
  action: 'up' | 'undo' | 'resume'
  /** The worktree it was run on: shown and acted on for that one only. */
  work: string
  path: string
  isOk: boolean
  /** The whole of it, for the dialog. */
  text: string
  /** A few words led by ✓ or ✗, for the line above the prompt. */
  brief: string
  /** The lease-protected push wt printed for the rebased branch, as argv. */
  pushCommand: string[] | null
  canUndo: boolean
}

export type Busy = 'idle' | 'up' | 'undo' | 'resume'

/** The person's last own input: when, and how it began (cut with an ellipsis). */
export type Input = { at: number; head: string }

/** The last review skill this conversation ran, and the input count then. */
export type Review = { skill: string; at: number; inputs: number }

declare module 'claude-code' {
  interface PluginState {
    wt: {
      plan: Plan | null
      busy: Busy
      outcome: Outcome | null
      /** The person's own inputs so far in this conversation. */
      inputs: number
      lastInput: Input | null
      lastReview: Review | null
      /** When the person's first own input of this conversation was. */
      firstInput: number | null
    }
  }
}
