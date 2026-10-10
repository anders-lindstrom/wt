import { atom, read, update } from 'claude-code'
import type { EngineInterface, Register } from 'claude-code'

import type { Busy, Input, Outcome, Plan, Review } from '../types'

// The worktree the session stands in, as `wt status --json` reports it: one
// line above the prompt in git's marks, the part of it that is wrong in the
// warning colour, and a small dialog (`/wt`, or a click on that line) where
// the keys act. Nothing is pinned under the prompt: the engine draws a mod's
// notice there as a warning whatever it says. Every action is a wt command the person pressed or
// typed; nothing here rebases, pushes or undoes by itself.
//
// Two knobs, off unless the person's settings turn them on, each a section of
// its own that reads nothing of wt's but the worktree's path: `gittree` (open
// the worktree in gittree) and `reviewStatus` (when this conversation last ran
// a review skill, and when the person last typed: above the prompt once each
// is old, and in the dialog).

type Engine = EngineInterface
type Loose = Record<string, any>
type Ran = { exitCode: number; stdout: string; stderr: string }
type Action = 'up' | 'upDiverged' | 'undo' | 'push' | 'resume' | 'fetch' | 'refresh' | 'details' | 'close' | 'dismiss' | 'gittree'
type Tone = 'suggestion' | 'success' | 'warning' | 'error'
// One part of the line above the prompt, in the colour that says how it is:
// none when it is as it should be.
type Part = { text: string; tone: Tone | 'dim' | undefined; isOwn?: true }
type Note = Part
// What the dialog shows for a worktree: what is not as it should be, and the
// keys, the one that moves the branch on a row of its own.
type Detail = { notes: Note[]; primary: Action | null; hint: string; follow: Action[]; other: Action[] }

const PANE = 'wt'
const TEN_MINUTES = 600_000
const PULSE_MS = 10_000
// Past this, what is shown about trunk says how old it is.
const STALE_MS = 60 * 60_000

const plan = atom({ plugin: 'wt', key: 'plan' } as const, null)
const busy = atom({ plugin: 'wt', key: 'busy' } as const, 'idle')
const outcome = atom({ plugin: 'wt', key: 'outcome' } as const, null)
const inputs = atom({ plugin: 'wt', key: 'inputs' } as const, 0)
const lastInput = atom({ plugin: 'wt', key: 'lastInput' } as const, null)
const lastReview = atom({ plugin: 'wt', key: 'lastReview' } as const, null)
const firstInput = atom({ plugin: 'wt', key: 'firstInput' } as const, null)

// The knobs as the person's settings have them; `register` fills them in.
// A skill that performs a review: "review" in its name, and not the one about
// receiving one.
const REVIEW_SKILLS = '^(?!.*receiving).*review'
const knobs = {
  hasGittree: false,
  hasReview: false,
  reviewSkills: new RegExp(REVIEW_SKILLS, 'i'),
  // How old the last input and the last review are before the line above
  // the prompt says them.
  inputAfterMs: 5 * 60_000,
  reviewAfterMs: 30 * 60_000,
}

const LABEL: Record<Action, string> = {
  up: 'wt up',
  upDiverged: 'wt up over the divergence',
  undo: 'undo',
  push: 'push…',
  resume: 'resume',
  fetch: 'fetch',
  refresh: 'refresh',
  details: '/wt',
  close: 'close',
  dismiss: 'dismiss',
  gittree: 'gittree',
}
// On the line above the prompt gittree is a word to click: the arrow says it
// opens somewhere else.
const GITTREE_OUT = 'gittree ↗'
// The dialog's keys are letters: it holds the keyboard while it is open.
const HOTKEY: Partial<Record<Action, string>> = {
  up: 'u',
  upDiverged: 'v',
  undo: 'z',
  push: 'p',
  resume: 'c',
  fetch: 'f',
  refresh: 'r',
  gittree: 'g',
  dismiss: 'd',
  close: 'q',
}
// Said beside the dialog's close key: how it goes away while it holds the
// keyboard, and how it gets the keyboard when it opened without it.
const CLOSING = 'Esc closes too'
const KEYLESS = 'click here, or ctrl+x then tab, for the keys'
const RUNNING: Record<Busy, string> = {
  idle: '',
  up: 'wt up is running: fetch, fast-forward, rebase…',
  undo: 'wt sync undo is running…',
  resume: 'wt sync resume is running…',
}

const parse = (text: string): Loose | null => {
  try {
    const value: unknown = JSON.parse(text)

    return value !== null && typeof value === 'object' ? (value as Loose) : null
  } catch {
    return null
  }
}

const short = (sha: unknown): string => (typeof sha === 'string' && sha !== '' ? sha.slice(0, 7) : '?')
const lastLine = (text: string): string => text.trim().split('\n').pop()?.trim() ?? ''

// How long ago, in the fewest words that stay true.
const ago = (now: number, at: number): string => {
  const minutes = Math.max(0, Math.round((now - at) / 60_000))

  if (minutes < 1) {
    return 'just now'
  }

  return minutes < 90
    ? `${minutes} min ago`
    : minutes < 2_880
      ? `${Math.round(minutes / 60)}h ago`
      : `${Math.round(minutes / 1_440)} days ago`
}

const inputsSince = (count: number): string => (count === 0 ? 'no input since' : count === 1 ? '1 input since' : `${count} inputs since`)

// What the review knob puts on the line above the prompt: the last input and
// the last review, each only once it is older than its limit. A conversation
// no review has run in is as old as its first input.
const lingerOf = (typed: Input | null, review: Review | null, first: number | null, count: number, now: number): string => {
  const parts: string[] = []

  if (typed !== null && now - typed.at >= knobs.inputAfterMs) {
    parts.push(`last input ${ago(now, typed.at)}: “${typed.head}”`)
  }

  if (review !== null) {
    if (now - review.at >= knobs.reviewAfterMs) {
      parts.push(`reviewed ${ago(now, review.at)}, ${inputsSince(count - review.inputs)}`)
    }
  } else if (first !== null && now - first >= knobs.reviewAfterMs) {
    parts.push(`not reviewed yet, ${count === 1 ? '1 input' : `${count} inputs`}`)
  }

  return parts.join(' · ')
}

const planOf = (stdout: string): Plan | null => {
  const s = parse(stdout)

  if (s === null || s.command !== 'status' || s.worktree === null || typeof s.worktree !== 'object') {
    return null
  }

  const w: Loose = s.worktree
  const own: Loose | null = w.ownRemote ?? null
  const stack: Loose[] = Array.isArray(s.stack) ? s.stack : []
  const sessions: Loose[] = Array.isArray(s.sessions) ? s.sessions : []

  return {
    work: String(w.work),
    branch: typeof w.branch === 'string' && w.branch !== '' ? w.branch : null,
    path: String(w.path),
    isMain: w.isMain === true,
    trunk: String(s.trunk ?? ''),
    trunkRef: String(s.trunkRef ?? s.trunk ?? ''),
    trunkFetchedAt: s.trunkRefUpdatedAt ?? null,
    gitDir: null,
    fetchedAt: null,
    trunkRemoteAhead: Number(s.trunkSync?.remoteAhead ?? 0),
    behind: typeof w.behind === 'number' ? w.behind : null,
    ahead: typeof w.ahead === 'number' ? w.ahead : null,
    tree: String(w.state ?? 'clean'),
    ownRemote:
      own === null
        ? null
        : {
            ref: own.ref ?? null,
            state: String(own.state),
            ahead: own.ahead ?? null,
            behind: own.behind ?? null,
            blocks: own.blocks ?? null,
            noPushReason: typeof own.noPushReason === 'string' ? own.noPushReason : null,
            fixCommand: Array.isArray(own.fixCommand) ? own.fixCommand.map(String) : null,
          },
    upEligible: s.upEligible === true,
    upIneligibleCode: s.upIneligibleCode ?? null,
    upIneligibleReason: s.upIneligibleReason ?? null,
    token: s.token ?? null,
    stack: stack.map(member => String(member.work)).filter(name => name !== String(w.work)),
    sessions: sessions.map(one => ({ name: String(one.name), kind: String(one.kind), state: String(one.state) })),
    sessionsError: typeof s.sessionsError === 'string' ? s.sessionsError : null,
    schemaVersion: String(s.schemaVersion ?? ''),
  }
}

// The remote a ref lives on: "origin" of "origin/feat/login".
const remoteOf = (ref: string): string => ref.split('/')[0] ?? ref

// The branch against trunk in marks: ↓ what trunk has that it lacks, ↑ its own
// on top, ✓ when it lacks nothing, ? when wt could not count.
const trunkMarks = (p: Plan): string =>
  p.behind === null ? '?' : `${p.behind > 0 ? `↓${p.behind}` : '✓'}${p.ahead !== null && p.ahead > 0 ? `↑${p.ahead}` : ''}`

// The branch against its own remote in the same marks, then the remote's
// name; undefined when there is nothing to say (in sync, or no ref of its own).
const ownMarks = (p: Plan): string | undefined => {
  const own = p.ownRemote

  if (own === null || own.ref === null) {
    return undefined
  }

  const remote = remoteOf(own.ref)

  switch (own.state) {
    case 'rebased':
      return `↑ ${remote} (rebased)`
    case 'ahead':
      return `↑${own.ahead ?? ''} ${remote}`
    case 'behind':
      return `↓${own.behind ?? ''} ${remote}`
    case 'diverged':
      return `↓${own.behind ?? ''}↑${own.ahead ?? ''} ${remote} diverged`
    case 'gone':
      return `✗ ${remote}`
    default:
      return undefined
  }
}

// The same in words, for the dialog.
const ownWords = (p: Plan): string | undefined => {
  const own = p.ownRemote

  if (own === null) {
    return undefined
  }

  // wt's own sentence, and the command it names for it.
  if (own.noPushReason !== null) {
    return `nowhere to push: ${own.noPushReason}${own.fixCommand === null ? '' : `. ${own.fixCommand.map(quoted).join(' ')} records the first`}`
  }

  if (own.ref === null) {
    return undefined
  }

  switch (own.state) {
    case 'rebased':
      return `rebased and not pushed to ${own.ref} yet`
    case 'ahead':
      return `${own.ahead} to push to ${own.ref}`
    case 'behind':
      return `${own.behind} behind ${own.ref}`
    case 'diverged':
      return `diverged from ${own.ref}: ${own.ahead} here, ${own.behind} there`
    case 'gone':
      return `${own.ref} is gone`
    case 'unknown':
      return `${own.ref} could not be checked`
    default:
      return undefined
  }
}

// How old what is known about trunk is, once that is worth saying: nothing
// here fetches, so a count against trunk is as of the last fetch anyone made.
const staleWords = (p: Plan, now: number): string | undefined =>
  p.fetchedAt !== null && now - p.fetchedAt > STALE_MS ? `fetched ${ago(now, p.fetchedAt)}` : undefined

const ownBehind = (p: Plan): number => (p.ownRemote?.state === 'behind' ? (p.ownRemote.behind ?? 0) : 0)
const behindTrunk = (p: Plan): number => p.behind ?? 0

// Why `wt up` would not run on it as it stands, or undefined when it would.
// Changes in the tree are not judged here: status counts untracked files and
// the run refuses tracked changes only, so the run is the one to say.
const obstacle = (p: Plan): string | undefined => {
  if (!p.upEligible) {
    return p.upIneligibleReason ?? p.upIneligibleCode ?? 'wt would refuse it'
  }

  const working = p.sessions.filter(one => one.state === 'busy').map(one => one.name)

  return working.length > 0 ? `another session is working in it (${working.join(', ')})` : undefined
}

const hasWork = (p: Plan): boolean => behindTrunk(p) > 0 || ownBehind(p) > 0
const canUp = (p: Plan): boolean => !p.isMain && p.token !== null && hasWork(p) && obstacle(p) === undefined

// The worktree as the parts of one line: against trunk, against its own
// remote, a rebase waiting, a run wt would refuse, how old the view of trunk
// is. What wt up would do something about is in the accent colour, and only
// while wt up would run; what is wrong is in the warning colour, and comes
// before what is dim, so that a narrow terminal cuts the dim part first.
const standing = (p: Plan, now: number): Part[] => {
  const parts: Part[] = []

  if (p.isMain) {
    parts.push({ text: p.trunkRemoteAhead > 0 ? `main checkout ↓${p.trunkRemoteAhead} ${remoteOf(p.trunkRef)}` : 'main checkout', tone: undefined })
  } else {
    const own = ownMarks(p)
    const state = p.ownRemote?.state
    const isHandedOver = p.upIneligibleCode === 'handedOver'
    const wouldRun = !isHandedOver && obstacle(p) === undefined
    // Uncommitted changes, as a git prompt marks them, after the last mark.
    const dirty = !isHandedOver && p.tree === 'dirty' ? ' *' : ''
    parts.push({
      text: `${p.work} ${trunkMarks(p)} ${p.trunk}${own === undefined ? dirty : ''}`,
      tone: wouldRun && behindTrunk(p) > 0 ? 'suggestion' : undefined,
    })

    if (own !== undefined) {
      parts.push({
        text: `${own}${dirty}`,
        tone: state === 'diverged' || state === 'gone' ? 'warning' : wouldRun && state === 'behind' ? 'suggestion' : undefined,
        isOwn: true,
      })
    }

    if (isHandedOver) {
      parts.push({ text: 'REBASE STOPPED', tone: 'warning' })
    } else if (!wouldRun && hasWork(p)) {
      parts.push({ text: 'wt up would refuse', tone: 'warning' })
    }
  }

  const stale = staleWords(p, now)

  if (stale !== undefined) {
    parts.push({ text: stale, tone: 'dim' })
  }

  return parts
}

// The same as text, for a command's answer.
const statusText = (p: Plan | null, now: number): string | undefined =>
  p === null
    ? undefined
    : standing(p, now)
        .map(part => part.text)
        .join(' · ')

// The line above the prompt, or null outside a worktree of wt's: a run under
// way, what the last run came to, or how the worktree stands.
const lineOf = (p: Plan | null, last: Outcome | null, state: Busy, now: number): Part[] | null => {
  if (state !== 'idle') {
    return [{ text: RUNNING[state], tone: 'suggestion' }]
  }

  // An outcome is shown, and acted on, only for the worktree it was run on.
  // It never hides what is wrong with the worktree as it stands now, and one
  // that succeeded is followed by what is left to push.
  if (last !== null && p !== null && last.path === p.path) {
    return [
      { text: last.brief, tone: last.isOk ? 'success' : 'error' },
      ...standing(p, now).filter(part => part.tone === 'warning' || (last.isOk && part.isOwn === true)),
    ]
  }

  if (p === null || p.isMain) {
    return null
  }

  return standing(p, now)
}

// What the dialog shows for the worktree; `mine` is the last outcome when it
// is this worktree's.
const detailOf = (p: Plan, mine: Outcome | null): Detail => {
  const notes: Note[] = []
  const isHandedOver = p.upIneligibleCode === 'handedOver'
  const own = ownWords(p)
  const why = p.isMain || isHandedOver ? undefined : obstacle(p)

  if (p.isMain && p.trunkRemoteAhead > 0) {
    notes.push({ text: `local ${p.trunk} is ${p.trunkRemoteAhead} behind ${p.trunkRef}`, tone: 'warning' })
  }

  if (own !== undefined) {
    const state = p.ownRemote?.state
    const isUnpushed = p.ownRemote?.noPushReason != null
    notes.push({ text: own, tone: state === 'diverged' || state === 'gone' ? 'error' : state === 'behind' || isUnpushed ? 'warning' : undefined })
  }

  if (!p.isMain && p.tree !== 'clean') {
    notes.push({ text: p.tree === 'dirty' ? '* uncommitted changes (untracked files count)' : `the tree is ${p.tree}`, tone: 'warning' })
  }

  if (p.stack.length > 0) {
    notes.push({ text: `moves with ${p.stack.join(', ')}`, tone: undefined })
  }

  if (p.sessionsError !== null) {
    notes.push({ text: `sessions could not be listed: ${p.sessionsError}`, tone: 'warning' })
  } else if (p.sessions.length > 0) {
    notes.push({ text: `sessions: ${p.sessions.map(one => `${one.name} (${one.kind}, ${one.state})`).join(', ')}`, tone: undefined })
  }

  // Why there is no key for wt up, for someone who looked for it: said only
  // when wt counted, and the count is none.
  if (!p.isMain && !isHandedOver && p.behind === 0 && ownBehind(p) === 0 && mine === null && p.upIneligibleCode !== 'ownRemoteDiverged') {
    notes.push({ text: 'not behind: wt up has nothing to do', tone: 'dim' })
  }

  if (isHandedOver) {
    notes.push({ text: 'a rebase stopped at a conflict and is yours: resolve it in the worktree, then resume; or undo it', tone: 'warning' })
  } else if (why !== undefined && (hasWork(p) || p.upIneligibleCode === 'ownRemoteDiverged')) {
    notes.push({ text: `wt up would refuse: ${why}`, tone: 'warning' })
  }

  let primary: Action | null = null
  let hint = ''
  const follow: Action[] = []

  if (isHandedOver) {
    primary = 'resume'
    hint = 'once the conflict is resolved'
    follow.push('undo')
  } else if (canUp(p)) {
    primary = 'up'
    hint = `fetches, then rebases onto ${p.trunkRef}; never pushes`
  } else if (p.upIneligibleCode === 'ownRemoteDiverged' && p.token !== null) {
    primary = 'upDiverged'
    hint = 'rebases although the branch diverged from its own remote'
  }

  if (mine !== null) {
    if (mine.pushCommand !== null) {
      follow.push('push')
    }

    if (mine.canUndo && !follow.includes('undo')) {
      follow.push('undo')
    }

    follow.push('dismiss')
  }

  return { notes, primary, hint, follow, other: knobs.hasGittree ? ['fetch', 'gittree', 'refresh'] : ['fetch', 'refresh'] }
}

const failed = (action: Outcome['action'], p: Plan, text: string, brief: string): Outcome => ({
  action,
  work: p.work,
  path: p.path,
  isOk: false,
  text,
  brief: `✗ ${brief}`,
  pushCommand: null,
  canUndo: false,
})

// The results that mean a participant is where the run meant to leave it.
const SETTLED: readonly string[] = ['rebased', 'fastForwarded', 'skipped', 'undone']
const RESULT_WORDS: Record<string, string> = {
  handedOver: 'stopped at a conflict',
  notRun: 'was not run',
  refused: 'was refused',
  restored: 'failed and was put back',
  needsRecovery: 'needs recovery',
  interrupted: 'was interrupted',
  rebasedStepFailed: 'was rebased, but a step after it failed',
}

// One wt run's JSON (`wt up`, `wt sync resume`, `wt sync undo`) as the sentence
// the dialog shows, the few words the line above the prompt shows, and what
// can follow it. A run moves the whole stack, so another member that did not
// settle makes the outcome a failure and takes the push away: the person
// sorts the stack out first.
const outcomeOf = (action: Outcome['action'], ran: Ran, p: Plan): Outcome => {
  const result = parse(ran.stdout)

  if (result === null) {
    return failed(action, p, lastLine(ran.stderr) || `wt exited ${ran.exitCode} and printed no result`, `wt ${action} failed for ${p.work}`)
  }

  const list: Loose[] = Array.isArray(result.worktrees) ? result.worktrees : []
  const mine = list.find(one => one.path === p.path) ?? list.find(one => one.work === p.work)
  const error = typeof result.error === 'string' && result.error !== '' ? result.error : null
  const reasonOf = (one: Loose): string => (typeof one.reason === 'string' && one.reason !== '' ? `: ${one.reason}` : '')

  // A run wt refused before it touched anything lists its participants as
  // not run and says why at the top.
  if (mine === undefined || String(mine.result) === 'notRun') {
    const why = error ?? (mine !== undefined && reasonOf(mine) !== '' ? reasonOf(mine).slice(2) : lastLine(ran.stderr))

    return failed(action, p, `wt did not touch ${p.work}${why === '' ? '' : `: ${why}`}`, `wt did not touch ${p.work}`)
  }

  const unsettled = list.filter(one => one !== mine && !SETTLED.includes(String(one.result)))
  const also =
    unsettled.length === 0
      ? ''
      : ` But in its stack ${unsettled.map(one => `${one.work} ${RESULT_WORDS[String(one.result)] ?? one.result}${reasonOf(one)}`).join('; ')}.`
  const range = `${short(mine.before)} → ${short(mine.after)}`
  const sync: Loose | null = mine.ownRemoteSync ?? null
  const forwarded = sync?.fastForwarded === true ? `, after a fast-forward to ${sync.ref}` : ''
  const pushCommand = unsettled.length === 0 && Array.isArray(mine.pushCommand) ? mine.pushCommand.map(String) : null
  const reason = reasonOf(mine)
  const recovery = typeof mine.recovery === 'string' && mine.recovery !== '' ? ` ${mine.recovery}` : ''
  const onto = String(result.trunkRef ?? p.trunkRef)
  const done = (text: string, brief: string, canUndo: boolean): Outcome => ({
    action,
    work: p.work,
    path: p.path,
    isOk: unsettled.length === 0,
    text: text + also,
    brief: unsettled.length === 0 ? `✓ ${brief}` : `✗ ${brief}, but its stack did not settle`,
    pushCommand,
    canUndo,
  })

  switch (String(mine.result)) {
    case 'rebased':
      return done(`${p.work} rebased onto ${onto}${forwarded} (${range}). Undo puts it back.`, `${p.work} rebased onto ${onto}`, true)
    case 'fastForwarded':
      return done(
        `${p.work} fast-forwarded to ${sync?.ref ?? 'its own remote'} (${range}); nothing was left to rebase.`,
        `${p.work} fast-forwarded`,
        true,
      )
    case 'undone':
      return done(`${p.work} is back where it was (${range}).`, `${p.work} is back where it was`, false)
    case 'skipped':
      return done(`${p.work}: nothing to do${reason}`, `${p.work}: nothing to do`, false)
    case 'handedOver':
      return {
        ...failed(
          action,
          p,
          `${p.work}: the rebase stopped at a conflict and is yours now. Resolve it in the worktree, then resume; or undo it.${also}`,
          `${p.work}: the rebase stopped at a conflict`,
        ),
        canUndo: true,
      }
    case 'refused':
      return failed(action, p, `wt refused ${p.work}${reason}${also}`, `wt refused ${p.work}`)
    case 'restored':
      return failed(action, p, `${p.work}: the rebase failed and wt put the branch back${reason}.${also}`, `${p.work}: the rebase failed, the branch is back`)
    case 'rebasedStepFailed':
    case 'interrupted':
      return {
        ...failed(action, p, `${p.work} ${RESULT_WORDS[String(mine.result)]}${reason}.${recovery}${also}`, `${p.work} ${RESULT_WORDS[String(mine.result)]}`),
        canUndo: true,
      }
    default:
      return failed(action, p, `${p.work}: ${mine.result}${reason}.${recovery}${also}`, `${p.work}: ${mine.result}`)
  }
}

let isChecking = false
let wantsAnother = false
// Whether a model turn runs: set by turn.start and turn.complete, and by the
// band wherever it is drawn. wt moves nothing under one.
let isTurnRunning = false
// The move under way, if any: one at a time, and a prompt sent meanwhile
// waits for it rather than start a turn on a branch in mid-rebase.
let moving: Promise<void> | null = null
// The fetch the person asked for, while it runs: a move waits for it.
let fetching: Promise<void> | null = null

// An argument as a shell reads it back unchanged.
const quoted = (arg: string): string => (/^[A-Za-z0-9_/.:=@%+,-]+$/.test(arg) ? arg : `'${arg.replace(/'/g, `'\\''`)}'`)

// When the repository last fetched anything: the age of FETCH_HEAD in its git
// directory, which every worktree of it shares. Read, never caused.
const fetched = async ($: Engine, path: string): Promise<Pick<Plan, 'gitDir' | 'fetchedAt'>> => {
  try {
    const ran = await $.process.run(['git', 'rev-parse', '--path-format=absolute', '--git-common-dir'], { cwd: path, timeoutMs: 5_000 })
    const gitDir = ran.exitCode === 0 ? ran.stdout.trim() : ''

    if (gitDir === '') {
      return { gitDir: null, fetchedAt: null }
    }

    try {
      return { gitDir, fetchedAt: (await $.fs.stat(`${gitDir}/FETCH_HEAD`)).mtimeMs }
    } catch {
      // Never fetched.
      return { gitDir, fetchedAt: null }
    }
  } catch {
    return { gitDir: null, fetchedAt: null }
  }
}

// Reads the worktree again. One `wt status` at a time: a second ask while
// one runs is served by one more run after it.
const refresh = async ($: Engine): Promise<Plan | null> => {
  if (isChecking) {
    wantsAnother = true

    return read($, plan)
  }

  isChecking = true

  try {
    // Read before the worktree is, so that what is read of the worktree is
    // from after the run it came from.
    const shown = await read($, outcome)
    let found: Plan | null = null

    try {
      found = planOf((await $.process.run(['wt', 'status', '--json'], { timeoutMs: 20_000 })).stdout)
    } catch {
      // wt is not installed, or did not answer in time: nothing to show.
    }

    if (found !== null) {
      found = { ...found, ...(await fetched($, found.path)) }
    }

    const held = await read($, plan)

    if (JSON.stringify(held) !== JSON.stringify(found)) {
      await update($, plan, () => found)
    }

    // A run that succeeded has nothing left to say once its branch is pushed.
    if (
      found !== null &&
      shown !== null &&
      shown.isOk &&
      shown.pushCommand !== null &&
      shown.path === found.path &&
      found.ownRemote?.state === 'inSync' &&
      JSON.stringify(await read($, outcome)) === JSON.stringify(shown)
    ) {
      await update($, outcome, () => null)
    }

    return found
  } finally {
    isChecking = false

    if (wantsAnother) {
      wantsAnother = false
      refresh($).catch(() => undefined)
    }
  }
}

// The refs the worktree's standing rests on, as they stood at the last pulse.
let pulse: string | null = null

// Keeps what is shown current for someone coming back to this terminal from
// another: every few seconds one cheap read of the branch, trunk and remote
// refs, and a full `wt status` only when one of them has moved. A fetch, a
// commit or a rebase made anywhere else shows here within one pulse.
const beat = async ($: Engine): Promise<void> => {
  const p = await read($, plan)

  if (p === null || moving !== null || isChecking) {
    return
  }

  const refs = [`refs/heads/${p.trunk}`, `refs/remotes/${p.trunkRef}`]

  if (p.branch !== null) {
    refs.push(`refs/heads/${p.branch}`)
  }

  if (p.ownRemote !== null && p.ownRemote.ref !== null) {
    refs.push(`refs/remotes/${p.ownRemote.ref}`)
  }

  const ran = await $.process.run(['git', 'for-each-ref', '--format=%(refname) %(objectname)', ...refs], { cwd: p.path, timeoutMs: 5_000 })

  if (ran.exitCode !== 0) {
    return
  }

  // A fetch that moved no ref of ours still makes what is shown newer.
  let fetchedAt = 0

  if (p.gitDir !== null) {
    try {
      fetchedAt = (await $.fs.stat(`${p.gitDir}/FETCH_HEAD`)).mtimeMs
    } catch {
      // Never fetched.
    }
  }

  const before = pulse
  pulse = `${p.path}\n${fetchedAt}\n${ran.stdout}`

  if (before !== null && before !== pulse) {
    await refresh($)
  }
}

// Tells the model what the person just did to the branch under it.
const tell = async ($: Engine, text: string): Promise<void> => {
  try {
    await $.session.append({ message: { type: 'user', content: [{ type: 'text', text }] } })
  } catch {
    // A run no plugin may shape: the band still says it.
  }
}

// The rows the dialog needs for what it has to show.
const rowsOf = async ($: Engine): Promise<number> => {
  const p = await read($, plan)
  const last = await read($, outcome)

  if (p === null) {
    return 5
  }

  const mine = last !== null && last.path === p.path ? last : null
  const detail = detailOf(p, mine)

  return (
    1 +
    detail.notes.length +
    (mine !== null ? 3 : 0) +
    1 +
    (detail.primary !== null ? 1 : 0) +
    (detail.follow.length > 0 ? 1 : 0) +
    1 +
    (knobs.hasReview ? 3 : 0) +
    2
  )
}

// Opens the dialog with the keyboard in it, so its letter keys answer and Esc
// closes it, as tall as what it has to show. With text in the prompt the keys
// stay there; Esc closes it once the prompt is empty.
const openPane = async ($: Engine): Promise<void> => {
  await $.ui.open({ id: PANE, title: 'wt', focus: true, closeOnEscape: true, rows: await rowsOf($) })
}

// Whether the dialog is the pane on show. One open behind another pane, or
// waiting undrawn, is not: asked for, that one is raised.
const isOnShow = async ($: Engine): Promise<boolean> => (await $.ui.panes()).some(one => one.id === PANE && one.isShown && one.isPlaced)

// Asks again for the rows an open dialog needs, now that a run has left a
// result and its keys to show: the rows, and never the keyboard, so that a
// letter typed at the prompt is not taken for one of its keys. Whether it is
// still on show is asked last, so that one just closed stays closed.
const resize = async ($: Engine): Promise<void> => {
  try {
    const rows = await rowsOf($)

    if (await isOnShow($)) {
      await $.ui.open({ id: PANE, title: 'wt', closeOnEscape: true, rows })
    }
  } catch {
    // The dialog scrolls instead.
  }
}

// Runs one action for the worktree. The moves leave an outcome to show.
const act = async ($: Engine, action: Action): Promise<string> => {
  if (action === 'details') {
    // A click on the line toggles, as bare /wt does: on show, it closes.
    if (await isOnShow($)) {
      await $.ui.close({ id: PANE })

      return 'wt pane closed.'
    }

    await openPane($)

    return 'wt pane opened.'
  }

  if (action === 'close') {
    await $.ui.close({ id: PANE })

    return 'wt pane closed.'
  }

  const p = await read($, plan)
  const last = await read($, outcome)

  if (action === 'dismiss') {
    // What the last run came to goes away; what is still to do comes back.
    await update($, outcome, () => null)

    return 'Dismissed.'
  }

  if (action === 'refresh') {
    return statusText(await refresh($), await $.clock.now()) ?? 'No wt worktree here.'
  }

  if (action === 'gittree') {
    // gittree shows the repository and worktree holding the path, launching
    // the app or bringing it forward; outside a wt worktree, the session's own.
    let text: string

    try {
      const ran = await $.process.run(['gittree', p?.path ?? '.'], { timeoutMs: 20_000 })
      text = ran.exitCode === 0 ? `Opened ${p?.work ?? 'this folder'} in gittree.` : `gittree: ${lastLine(ran.stderr) || `exited ${ran.exitCode}`}`
    } catch (error) {
      text = `gittree did not start: ${error instanceof Error ? error.message : String(error)}`
    }

    $.ui.toast(text)

    return text
  }

  if (p === null) {
    return 'No wt worktree here.'
  }

  if (action === 'fetch') {
    // wt up fetches too, and two fetches at once clash over a ref's lock.
    if (moving !== null) {
      return 'wt is already running here.'
    }

    if (fetching !== null) {
      return 'A fetch is already running here.'
    }

    let text: string
    let release = (): void => undefined

    fetching = new Promise<void>(resolve => {
      release = resolve
    })

    try {
      const ran = await $.process.run(['git', 'fetch', '--quiet', 'origin', p.trunk], { cwd: p.path, timeoutMs: 120_000 })
      text = ran.exitCode === 0 ? `Fetched ${p.trunkRef}.` : `The fetch failed: ${lastLine(ran.stderr) || `git exited ${ran.exitCode}`}`
    } catch (error) {
      text = `The fetch did not finish: ${error instanceof Error ? error.message : String(error)}`
    } finally {
      fetching = null
      release()
    }

    await refresh($)
    $.ui.toast(text)

    return text
  }

  if (action === 'push') {
    // The push is the person's to run: it goes in the prompt as a shell
    // line, so they see it, press Enter, and the repository's git hooks run.
    if (last === null || last.pushCommand === null || last.path !== p.path) {
      return 'Nothing to push from here: run wt up first.'
    }

    const line = `! ${last.pushCommand.map(quoted).join(' ')}`
    let isFilled = false

    // The prompt takes nothing while a dialog holds the keys, and the push is
    // run from the prompt: the dialog closes first.
    try {
      await $.ui.close({ id: PANE })
      isFilled = (await $.prompt.fill({ text: line })).isFilled
    } catch {
      // Said below, with the command.
    }

    const text = isFilled ? 'The push is in the prompt: press Enter to run it.' : `The prompt did not take the push. Run it yourself: ${line}`
    $.ui.toast(text)

    return text
  }

  if (moving !== null) {
    return 'wt is already running here.'
  }

  if (isTurnRunning) {
    const text = `${LABEL[action]}: a turn is running in ${p.work}. wt can move it once the turn is done.`
    $.ui.toast(text)

    return text
  }

  let argv: string[]

  switch (action) {
    case 'up':
    case 'upDiverged':
      if (p.token === null) {
        return `wt up would refuse ${p.work}: ${obstacle(p) ?? 'it has no plan'}`
      }

      argv = ['wt', 'up', '--yes', '--no-push', '--json', '--expect', p.token]

      if (action === 'upDiverged') {
        argv.push('--allow-diverged')
      }

      break
    case 'undo':
      // "." is the worktree the command runs in: a bare work name can be two.
      argv = ['wt', 'sync', 'undo', '.', '--yes', '--json']
      break
    case 'resume':
      argv = ['wt', 'sync', 'resume', '.', '--yes', '--no-push', '--json']
      break
  }

  const kind: Outcome['action'] = action === 'upDiverged' ? 'up' : action
  let release = (): void => undefined
  let next: Outcome

  // Taken before the first await, so a second press finds it.
  moving = new Promise<void>(resolve => {
    release = resolve
  })

  try {
    await update($, busy, () => kind)

    if (fetching !== null) {
      await fetching
    }

    try {
      next = outcomeOf(kind, await $.process.run(argv, { cwd: p.path, timeoutMs: TEN_MINUTES }), p)
    } catch (error) {
      next = failed(
        kind,
        p,
        `${argv.slice(0, 3).join(' ')} did not finish: ${error instanceof Error ? error.message : String(error)}`,
        `${argv.slice(0, 3).join(' ')} did not finish`,
      )
    }

    await update($, outcome, () => next)
  } finally {
    await update($, busy, () => 'idle').catch(() => undefined)
    moving = null
    release()
  }

  await refresh($)
  await resize($)
  await tell(
    $,
    `[wt plugin] The person ran ${argv.slice(0, 3).join(' ')} on the worktree ${p.work} (${p.branch ?? 'detached'}). ${next.text}` +
      (next.isOk ? ' Its files and commits may have changed: read again before editing.' : ''),
  )

  return next.text
}

export const register: Register = (on, options) => {
  knobs.hasGittree = options.gittree === true
  knobs.hasReview = options.reviewStatus === true
  knobs.inputAfterMs = (typeof options.inputAfterMinutes === 'number' && options.inputAfterMinutes >= 0 ? options.inputAfterMinutes : 5) * 60_000
  knobs.reviewAfterMs = (typeof options.reviewAfterMinutes === 'number' && options.reviewAfterMinutes >= 0 ? options.reviewAfterMinutes : 30) * 60_000

  try {
    knobs.reviewSkills = new RegExp(typeof options.reviewSkills === 'string' && options.reviewSkills !== '' ? options.reviewSkills : REVIEW_SKILLS, 'i')
  } catch {
    knobs.reviewSkills = new RegExp(REVIEW_SKILLS, 'i')
  }

  on('session.start', async ($, e, next) => {
    await $.command.register({
      name: 'wt',
      description: 'The worktree you are in: its place against trunk, and wt up, undo, push from here',
      argumentHint: knobs.hasGittree
        ? '[up|undo|push|resume|fetch|refresh|dismiss|close|gittree]'
        : '[up|undo|push|resume|fetch|refresh|dismiss|close]',
    })
    // A reload ends whatever this module was waiting on: nothing runs now.
    await update($, busy, () => 'idle')
    // Nothing of this mod's is pinned under the prompt; an older load's goes.
    $.ui.status(undefined)
    refresh($).catch(() => undefined)
    // What is shown counts time: a minute later it says a minute more.
    $.clock.every(60_000, () => {
      $.ui.invalidate('ui.render')
    })
    $.clock.every(PULSE_MS, () => {
      beat($).catch(() => undefined)
    })
    // What no ref shows (a file edited from outside, a session come or gone)
    // is caught by the full read.
    $.clock.every(180_000, () => {
      refresh($).catch(() => undefined)
    })

    return next(e)
  })

  // Whatever a turn did to the checkout shows once it is done, and after each
  // shell command that could have moved a branch.
  on('tool.call', { tool: 'Bash' }, async ($, e, next) => {
    const ran = await next(e)

    if (/\b(git|wt|gh)\b/.test(e.command)) {
      refresh($).catch(() => undefined)
    }

    return ran
  }).catch(($, e, next) => next(e))

  on('turn.start', ($, e, next) => {
    isTurnRunning = true

    return next(e)
  })

  on('turn.complete', ($, e, next) => {
    isTurnRunning = false
    refresh($).catch(() => undefined)

    return next(e)
  })

  // A new conversation starts with no review and no input behind it.
  on('session.end', async ($, e, next) => {
    if (e.reason === 'clear') {
      await update($, inputs, () => 0)
      await update($, lastInput, () => null)
      await update($, lastReview, () => null)
      await update($, firstInput, () => null)
    }

    return next(e)
  })

  // The review knob: the person's own inputs, counted and remembered by how
  // they began, and each review skill as it is expanded for the model, typed
  // as /name or called through the Skill tool alike.
  on('prompt.submit', async ($, e, next) => {
    // A prompt sent while wt moves the branch waits for the move to end.
    if (moving !== null) {
      await moving
    }

    // The person's own: typed here, sent from a phone, or typed in an app
    // that hosts the session.
    const isOwn = e.origin.kind === 'composer' || e.origin.kind === 'bridge' || e.origin.kind === 'sdk'

    // A run that succeeded and left nothing to push has been seen once the
    // person types on: the line goes back to how the worktree stands.
    if (isOwn && !e.text.trimStart().startsWith('/')) {
      const last = await read($, outcome)

      if (last !== null && last.isOk && last.pushCommand === null) {
        await update($, outcome, () => null)
      }
    }

    if (knobs.hasReview && isOwn) {
      const letters = Array.from(e.text.replace(/\s+/g, ' ').trim())
      const now = await $.clock.now()
      const typed: Input = { at: now, head: letters.length > 20 ? `${letters.slice(0, 20).join('')}…` : letters.join('') }
      const count = (await read($, inputs)) + 1
      await update($, inputs, () => count)
      await update($, lastInput, () => typed)

      if ((await read($, firstInput)) === null) {
        await update($, firstInput, () => now)
      }

      // A review skill expanded just before its own /name arrived here belongs
      // to this input, not to the one before it.
      const review = await read($, lastReview)

      if (review !== null && now - review.at < 5_000 && e.text.trimStart().startsWith(`/${review.skill}`)) {
        await update($, lastReview, () => ({ ...review, inputs: count }))
      }
    }

    return next(e)
  }).catch(($, e, next) => next(e))

  on('skill.prompt', async ($, e, next) => {
    if (knobs.hasReview && knobs.reviewSkills.test(e.skill)) {
      const ran: Review = { skill: e.skill, at: await $.clock.now(), inputs: await read($, inputs) }
      await update($, lastReview, () => ran)
    }

    return next(e)
  })

  on('command.run', { command: 'wt' }, async ($, e) => {
    const word = e.args.trim().split(/\s+/)[0] ?? ''
    const known: readonly Action[] = knobs.hasGittree
      ? ['up', 'undo', 'push', 'resume', 'fetch', 'refresh', 'dismiss', 'close', 'gittree']
      : ['up', 'undo', 'push', 'resume', 'fetch', 'refresh', 'dismiss', 'close']
    const action = known.find(one => one === word)

    if (action !== undefined) {
      return { text: await act($, action) }
    }

    if (word !== '') {
      return { text: `/wt ${word}: not one of ${known.join(', ')}.` }
    }

    // Bare, it toggles: the dialog on show is the dialog closed.
    if (await isOnShow($)) {
      return { text: await act($, 'close') }
    }

    const p = await refresh($)
    await openPane($)

    return { text: statusText(p, await $.clock.now()) ?? 'No wt worktree here.' }
  })

  on('ui.render', { component: 'AbovePrompt' }, async ($, e, next) => {
    isTurnRunning = e.props.isWorking

    if (e.props.hasSurvey) {
      return next(e)
    }

    const now = await $.clock.now()
    const line = lineOf(await read($, plan), await read($, outcome), await read($, busy), now)
    const linger = knobs.hasReview
      ? lingerOf(await read($, lastInput), await read($, lastReview), await read($, firstInput), await read($, inputs), now)
      : ''

    // The line takes no keys: the only ones that would answer from the prompt
    // are digits, and a digit typed into an empty prompt is the person's to
    // type. It names the command instead, and a click on that toggles it.
    //
    // Outside a worktree of wt's, with nothing old to recall and no gittree,
    // nothing is drawn.
    if (line === null && linger === '' && !knobs.hasGittree) {
      return next(e)
    }

    const { Box, Button, Text } = $.ui.resolve(e)

    return (
      <Box gap={1}>
        {line !== null && <Button key="details" plain label={LABEL.details} onPress={() => act($, 'details')} />}
        <Text wrap="truncate-end">
          {line !== null &&
            line.map((part, at) => [
              at === 0 ? '· ' : ' · ',
              <Text color={part.tone === 'dim' ? undefined : part.tone} dimColor={part.tone === 'dim'}>
                {part.text}
              </Text>,
            ])}
          {linger !== '' && line !== null && ' · '}
          {linger !== '' && <Text dimColor>{linger}</Text>}
        </Text>
        <Box flexGrow={1} />
        {knobs.hasGittree && <Button key="gittree" plain label={GITTREE_OUT} onPress={() => act($, 'gittree')} />}
      </Box>
    )
  })

  on('ui.render', { component: 'Pane', requestId: PANE }, async ($, e) => {
    const { Box, Button, Text } = $.ui.resolve(e)
    const p = await read($, plan)
    const last = await read($, outcome)
    const state = await read($, busy)
    const keyFor = (one: Action) => <Button key={one} plain label={LABEL[one]} hotkey={HOTKEY[one]} onPress={() => act($, one)} />
    const footer = (
      <Box gap={2} marginTop={1}>
        <Button key="close" plain dimColor role="dismiss" label={LABEL.close} hotkey={HOTKEY.close} onPress={() => act($, 'close')} />
        <Text dimColor>{e.props.isFocused ? CLOSING : KEYLESS}</Text>
      </Box>
    )

    if (p === null) {
      return (
        <Box flexDirection="column">
          <Text dimColor wrap="wrap">
            No wt worktree here: this folder is not in a repository wt knows, or wt is not installed.
          </Text>
          <Box gap={3} marginTop={1}>
            {keyFor('refresh')}
            {knobs.hasGittree && keyFor('gittree')}
          </Box>
          {footer}
        </Box>
      )
    }

    const now = await $.clock.now()
    // The last outcome, when it is this worktree's.
    const mine = last !== null && last.path === p.path ? last : null
    const detail = detailOf(p, mine)
    const review = knobs.hasReview ? await read($, lastReview) : null
    const typed = knobs.hasReview ? await read($, lastInput) : null
    const count = knobs.hasReview ? await read($, inputs) : 0

    return (
      <Box flexDirection="column">
        <Box gap={2}>
          <Text bold>{p.work}</Text>
          {p.isMain ? (
            <Text dimColor>main checkout</Text>
          ) : (
            <Text color={p.behind === null || p.behind > 0 ? 'warning' : 'success'}>{`${trunkMarks(p)} ${p.trunk}`}</Text>
          )}
          <Text dimColor>{p.fetchedAt === null ? 'fetch time unknown' : `fetched ${ago(now, p.fetchedAt)}`}</Text>
        </Box>
        {detail.notes.map(note => (
          <Text color={note.tone === 'dim' ? undefined : note.tone} dimColor={note.tone === 'dim'} wrap="wrap">
            {note.text}
          </Text>
        ))}
        {state !== 'idle' && (
          <Box marginTop={1}>
            <Text color="suggestion">{RUNNING[state]}</Text>
          </Box>
        )}
        {state === 'idle' && mine !== null && (
          <Box marginTop={1}>
            <Text color={mine.isOk ? 'success' : 'error'} wrap="wrap">
              {mine.text}
            </Text>
          </Box>
        )}
        <Box flexDirection="column" marginTop={1}>
          {state === 'idle' && detail.primary !== null && (
            <Box gap={3}>
              {keyFor(detail.primary)}
              <Text dimColor wrap="truncate-end">
                {detail.hint}
              </Text>
            </Box>
          )}
          {state === 'idle' && detail.follow.length > 0 && <Box gap={3}>{detail.follow.map(keyFor)}</Box>}
          {state === 'idle' && <Box gap={3}>{detail.other.map(keyFor)}</Box>}
        </Box>
        {knobs.hasReview && (
          <Box flexDirection="column" marginTop={1}>
            <Box gap={1}>
              <Box width={11}>
                <Text dimColor>review</Text>
              </Box>
              <Text color={review === null ? 'warning' : undefined} wrap="wrap">
                {review === null
                  ? 'no review skill has run in this conversation'
                  : `/${review.skill}, ${ago(now, review.at)}, ${inputsSince(count - review.inputs)}`}
              </Text>
            </Box>
            <Box gap={1}>
              <Box width={11}>
                <Text dimColor>last input</Text>
              </Box>
              <Text wrap="wrap">
                {typed === null ? 'none yet' : `${ago(now, typed.at)}: “${typed.head}” (${count} so far)`}
              </Text>
            </Box>
          </Box>
        )}
        {footer}
      </Box>
    )
  })
}
