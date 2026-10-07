import { atom, read, update } from 'claude-code'
import type { EngineInterface, Register } from 'claude-code'

import type { Busy, Input, Outcome, Plan, Review } from '../types'

// The worktree the session stands in, as `wt status --json` reports it: a
// status line, a band above the prompt when there is something to do about it,
// a pane with the detail, and `/wt`. Every action is a wt command the person
// pressed or typed; nothing here rebases, pushes or undoes by itself.
//
// Two knobs, off unless the person's settings turn them on, each a section of
// its own that reads nothing of wt's but the worktree's path: `gittree` (open
// the worktree in gittree) and `reviewStatus` (when this conversation last ran
// a review skill, and when the person last typed).

type Engine = EngineInterface
type Loose = Record<string, any>
type Ran = { exitCode: number; stdout: string; stderr: string }
type Action = 'up' | 'upDiverged' | 'undo' | 'push' | 'resume' | 'fetch' | 'refresh' | 'details' | 'hide' | 'gittree'
type Tone = 'suggestion' | 'success' | 'warning' | 'error'
type Offer = { tone: Tone; text: string; actions: Action[] }

const PANE = 'wt'
const TEN_MINUTES = 600_000
const PULSE_MS = 10_000
// Past this, what is shown about trunk says how old it is.
const STALE_MS = 60 * 60_000

const plan = atom({ plugin: 'wt', key: 'plan' } as const, null)
const busy = atom({ plugin: 'wt', key: 'busy' } as const, 'idle')
const outcome = atom({ plugin: 'wt', key: 'outcome' } as const, null)
const hiddenFor = atom({ plugin: 'wt', key: 'hiddenFor' } as const, null)
const inputs = atom({ plugin: 'wt', key: 'inputs' } as const, 0)
const lastInput = atom({ plugin: 'wt', key: 'lastInput' } as const, null)
const lastReview = atom({ plugin: 'wt', key: 'lastReview' } as const, null)

// The knobs as the person's settings have them; `register` fills them in.
// A skill that performs a review: "review" in its name, and not the one about
// receiving one.
const REVIEW_SKILLS = '^(?!.*receiving).*review'
const knobs = { hasGittree: false, hasReview: false, reviewSkills: new RegExp(REVIEW_SKILLS, 'i') }

const LABEL: Record<Action, string> = {
  up: 'wt up',
  upDiverged: 'Up over the divergence',
  undo: 'Undo',
  push: 'Push…',
  resume: 'Resume',
  fetch: 'Fetch trunk',
  refresh: 'Refresh',
  details: 'Details',
  hide: 'Hide',
  gittree: 'gittree',
}
const HOTKEY: Partial<Record<Action, string>> = { up: 'u', undo: 'z', push: 'p', resume: 'r', details: 'd', hide: 'h', gittree: 'g' }
// What moves a branch: never run while a model turn runs, and one at a time.
const MOVES: readonly Action[] = ['up', 'upDiverged', 'undo', 'resume']
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
      ? `${Math.round(minutes / 60)} h ago`
      : `${Math.round(minutes / 1_440)} days ago`
}

const inputsSince = (count: number): string => (count === 0 ? 'no input since' : count === 1 ? '1 input since' : `${count} inputs since`)

// The review knob's entry on the status line.
const reviewWords = (review: Review | null, count: number, now: number): string =>
  review === null ? 'not reviewed yet' : `reviewed ${ago(now, review.at)}, ${inputsSince(count - review.inputs)}`

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

// The branch against its own remote, in a few words; undefined when there is
// nothing to say (in sync, or no ref of its own).
const ownWords = (p: Plan, isShort: boolean): string | undefined => {
  const own = p.ownRemote

  if (own === null || own.ref === null) {
    return undefined
  }

  switch (own.state) {
    case 'rebased':
      return isShort ? 'not pushed' : `rebased and not pushed to ${own.ref} yet`
    case 'ahead':
      return isShort ? 'not pushed' : `${own.ahead} to push to ${own.ref}`
    case 'behind':
      return isShort ? `${own.behind} behind its remote` : `${own.behind} behind ${own.ref}`
    case 'diverged':
      return isShort ? 'diverged from its remote' : `diverged from ${own.ref}: ${own.ahead} here, ${own.behind} there`
    case 'gone':
      return isShort ? 'its remote branch is gone' : `${own.ref} is gone`
    case 'unknown':
      return isShort ? undefined : `${own.ref} could not be checked`
    default:
      return isShort ? undefined : `in sync with ${own.ref}`
  }
}

// The worktree in a few plain words. The engine leads the entry with the
// mod's name, so this never says "wt" itself.
// How old what is known about trunk is, once that is worth saying: nothing
// here fetches, so a count against trunk is as of the last fetch anyone made.
const staleWords = (p: Plan, now: number): string | undefined =>
  p.fetchedAt !== null && now - p.fetchedAt > STALE_MS ? `last fetched ${ago(now, p.fetchedAt)}` : undefined

const statusText = (p: Plan | null, now: number): string | undefined => {
  if (p === null) {
    return undefined
  }

  const stale = staleWords(p, now)

  if (p.isMain) {
    return [
      'main checkout',
      p.trunkRemoteAhead > 0 ? `local ${p.trunk} is ${p.trunkRemoteAhead} behind ${p.trunkRef}` : undefined,
      stale,
    ]
      .filter(part => part !== undefined)
      .join(' · ')
  }

  const parts = [
    p.work,
    p.behind === null ? `not compared with ${p.trunk}` : p.behind > 0 ? `${p.behind} behind ${p.trunk}` : `up to date with ${p.trunk}`,
  ]
  const own = ownWords(p, true)

  if (own !== undefined) {
    parts.push(own)
  }

  if (p.upIneligibleCode === 'handedOver') {
    parts.push('rebase waiting for you')
  } else if (p.tree === 'dirty') {
    parts.push('uncommitted changes')
  }

  if (stale !== undefined) {
    parts.push(stale)
  }

  return parts.join(' · ')
}

const ownBehind = (p: Plan): number => (p.ownRemote?.state === 'behind' ? (p.ownRemote.behind ?? 0) : 0)
const behindTrunk = (p: Plan): number => p.behind ?? 0

// How far the branch is from where `wt up` would put it, in words.
const distance = (p: Plan): string =>
  [
    behindTrunk(p) > 0 ? `${p.behind} behind ${p.trunkRef}` : '',
    ownBehind(p) > 0 ? `${ownBehind(p)} behind ${p.ownRemote?.ref ?? 'its own remote'}` : '',
  ]
    .filter(part => part !== '')
    .join(' and ')

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

const signature = (p: Plan | null): string =>
  p === null ? '' : [p.path, p.behind, ownBehind(p), p.upIneligibleCode].join('|')

const offerOf = (p: Plan | null, last: Outcome | null, state: Busy, now: number): Offer | null => {
  if (state !== 'idle') {
    return { tone: 'suggestion', text: RUNNING[state], actions: [] }
  }

  const isHandedOver = p !== null && p.upIneligibleCode === 'handedOver'

  // An outcome is shown, and acted on, only for the worktree it was run on.
  if (last !== null && p !== null && last.path === p.path) {
    const actions: Action[] = []

    if (isHandedOver) {
      actions.push('resume')
    } else if (!last.isOk && last.action === 'up' && canUp(p)) {
      actions.push('up')
    }

    if (last.pushCommand !== null) {
      actions.push('push')
    }

    if (last.canUndo || isHandedOver) {
      actions.push('undo')
    }

    return { tone: last.isOk ? 'success' : 'error', text: last.text, actions: [...actions, 'details', 'hide'] }
  }

  if (p === null || p.isMain) {
    return null
  }

  if (isHandedOver) {
    return {
      tone: 'warning',
      text: `${p.work}: a rebase stopped at a conflict and was handed to you. Resolve it in the worktree, then resume; or undo it.`,
      actions: ['resume', 'undo', 'details'],
    }
  }

  if (!hasWork(p)) {
    return null
  }

  const why = obstacle(p)
  const withStack = p.stack.length > 0 ? ` wt up moves it together with ${p.stack.join(', ')}.` : ''
  const stale = staleWords(p, now)
  // wt up fetches before it rebases, so an old count only understates the run.
  const asOf = stale === undefined ? '' : ` as of the fetch ${ago(now, p.fetchedAt ?? now)}`

  return why === undefined
    ? { tone: 'suggestion', text: `${p.work} is ${distance(p)}${asOf}.${withStack}`, actions: ['up', 'details', 'hide'] }
    : { tone: 'warning', text: `${p.work} is ${distance(p)}${asOf}, and wt up would refuse: ${why}`, actions: ['details', 'hide'] }
}

const failed = (action: Outcome['action'], p: Plan, text: string): Outcome => ({
  action,
  work: p.work,
  path: p.path,
  isOk: false,
  text,
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

// One wt run's JSON (`wt up`, `wt sync resume`, `wt sync undo`) as the line the
// band shows and what can follow it. A run moves the whole stack, so another
// member that did not settle makes the outcome a failure and takes the push
// away: the person sorts the stack out first.
const outcomeOf = (action: Outcome['action'], ran: Ran, p: Plan): Outcome => {
  const result = parse(ran.stdout)

  if (result === null) {
    return failed(action, p, lastLine(ran.stderr) || `wt exited ${ran.exitCode} and printed no result`)
  }

  const list: Loose[] = Array.isArray(result.worktrees) ? result.worktrees : []
  const mine = list.find(one => one.path === p.path) ?? list.find(one => one.work === p.work)
  const error = typeof result.error === 'string' && result.error !== '' ? result.error : null
  const reasonOf = (one: Loose): string => (typeof one.reason === 'string' && one.reason !== '' ? `: ${one.reason}` : '')

  // A run wt refused before it touched anything lists its participants as
  // not run and says why at the top.
  if (mine === undefined || String(mine.result) === 'notRun') {
    const why = error ?? (mine !== undefined && reasonOf(mine) !== '' ? reasonOf(mine).slice(2) : lastLine(ran.stderr))

    return failed(action, p, `wt did not touch ${p.work}${why === '' ? '' : `: ${why}`}`)
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
  const done = (text: string, canUndo: boolean): Outcome => ({
    action,
    work: p.work,
    path: p.path,
    isOk: unsettled.length === 0,
    text: text + also,
    pushCommand,
    canUndo,
  })

  switch (String(mine.result)) {
    case 'rebased':
      return done(`${p.work} rebased onto ${result.trunkRef ?? p.trunkRef}${forwarded} (${range}). Undo puts it back.`, true)
    case 'fastForwarded':
      return done(`${p.work} fast-forwarded to ${sync?.ref ?? 'its own remote'} (${range}); nothing was left to rebase.`, true)
    case 'undone':
      return done(`${p.work} is back where it was (${range}).`, false)
    case 'skipped':
      return done(`${p.work}: nothing to do${reason}`, false)
    case 'handedOver':
      return {
        ...failed(action, p, `${p.work}: the rebase stopped at a conflict and is yours now. Resolve it in the worktree, then resume; or undo it.${also}`),
        canUndo: true,
      }
    case 'refused':
      return failed(action, p, `wt refused ${p.work}${reason}${also}`)
    case 'restored':
      return failed(action, p, `${p.work}: the rebase failed and wt put the branch back${reason}.${also}`)
    case 'rebasedStepFailed':
    case 'interrupted':
      return { ...failed(action, p, `${p.work} ${RESULT_WORDS[String(mine.result)]}${reason}.${recovery}${also}`), canUndo: true }
    default:
      return failed(action, p, `${p.work}: ${mine.result}${reason}.${recovery}${also}`)
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

// An argument as a shell reads it back unchanged.
const quoted = (arg: string): string => (/^[A-Za-z0-9_/.:=@%+,-]+$/.test(arg) ? arg : `'${arg.replace(/'/g, `'\\''`)}'`)

// Pins the status line: the worktree, then the review knob's entry.
const pin = async ($: Engine): Promise<void> => {
  const now = await $.clock.now()
  const parts = [statusText(await read($, plan), now)]

  if (knobs.hasReview) {
    parts.push(reviewWords(await read($, lastReview), await read($, inputs), now))
  }

  const text = parts.filter(part => part !== undefined).join(' · ')
  $.ui.status(text === '' ? undefined : text)
}

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

// Reads the worktree again and pins the status line. One `wt status` at a
// time: a second ask while one runs is served by one more run after it.
const refresh = async ($: Engine): Promise<Plan | null> => {
  if (isChecking) {
    wantsAnother = true

    return read($, plan)
  }

  isChecking = true

  try {
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

    await pin($)

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

// Runs one action for the worktree. The moves leave an outcome for the band.
const act = async ($: Engine, action: Action): Promise<string> => {
  if (action === 'details') {
    await $.ui.open({ id: PANE, title: 'wt' })

    return 'wt pane opened.'
  }

  const p = await read($, plan)
  const last = await read($, outcome)

  if (action === 'hide') {
    // With an outcome on show, this dismisses it and what is still to do
    // comes back; without one, the band stays away until the worktree changes.
    if (last !== null) {
      await update($, outcome, () => null)

      return 'Dismissed.'
    }

    await update($, hiddenFor, () => signature(p))

    return 'Hidden until the worktree changes.'
  }

  if (action === 'refresh') {
    await update($, hiddenFor, () => null)

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
    let text: string

    try {
      const ran = await $.process.run(['git', 'fetch', '--quiet', 'origin', p.trunk], { cwd: p.path, timeoutMs: 120_000 })
      text = ran.exitCode === 0 ? `Fetched ${p.trunkRef}.` : `The fetch failed: ${lastLine(ran.stderr) || `git exited ${ran.exitCode}`}`
    } catch (error) {
      text = `The fetch did not finish: ${error instanceof Error ? error.message : String(error)}`
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

    await $.prompt.fill({ text: `! ${last.pushCommand.map(quoted).join(' ')}` })
    const text = 'The push is in the prompt: press Enter to run it.'
    $.ui.toast(text)

    return text
  }

  if (moving !== null) {
    return 'wt is already running here.'
  }

  if (isTurnRunning) {
    const text = `${LABEL[action]}: a turn is running in ${p.work}. Try again when it is done.`
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

    try {
      next = outcomeOf(kind, await $.process.run(argv, { cwd: p.path, timeoutMs: TEN_MINUTES }), p)
    } catch (error) {
      next = failed(kind, p, `${argv.slice(0, 3).join(' ')} did not finish: ${error instanceof Error ? error.message : String(error)}`)
    }

    await update($, outcome, () => next)
    await update($, hiddenFor, () => null)
  } finally {
    await update($, busy, () => 'idle').catch(() => undefined)
    moving = null
    release()
  }

  await refresh($)
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

  try {
    knobs.reviewSkills = new RegExp(typeof options.reviewSkills === 'string' && options.reviewSkills !== '' ? options.reviewSkills : REVIEW_SKILLS, 'i')
  } catch {
    knobs.reviewSkills = new RegExp(REVIEW_SKILLS, 'i')
  }

  on('session.start', async ($, e, next) => {
    await $.command.register({
      name: 'wt',
      description: 'The worktree you are in: its place against trunk, and wt up, undo, push from here',
      argumentHint: knobs.hasGittree ? '[up|undo|push|resume|fetch|refresh|gittree]' : '[up|undo|push|resume|fetch|refresh]',
    })
    // A reload ends whatever this module was waiting on: nothing runs now.
    await update($, busy, () => 'idle')
    refresh($).catch(() => undefined)
    // What is shown counts time: a minute later it says a minute more.
    $.clock.every(60_000, () => {
      pin($).catch(() => undefined)
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
    if (knobs.hasReview && (e.origin.kind === 'composer' || e.origin.kind === 'bridge' || e.origin.kind === 'sdk')) {
      const letters = Array.from(e.text.replace(/\s+/g, ' ').trim())
      const now = await $.clock.now()
      const typed: Input = { at: now, head: letters.length > 20 ? `${letters.slice(0, 20).join('')}…` : letters.join('') }
      const count = (await read($, inputs)) + 1
      await update($, inputs, () => count)
      await update($, lastInput, () => typed)

      // A review skill expanded just before its own /name arrived here belongs
      // to this input, not to the one before it.
      const review = await read($, lastReview)

      if (review !== null && now - review.at < 5_000 && e.text.trimStart().startsWith(`/${review.skill}`)) {
        await update($, lastReview, () => ({ ...review, inputs: count }))
      }

      await pin($)
    }

    return next(e)
  }).catch(($, e, next) => next(e))

  on('skill.prompt', async ($, e, next) => {
    if (knobs.hasReview && knobs.reviewSkills.test(e.skill)) {
      const ran: Review = { skill: e.skill, at: await $.clock.now(), inputs: await read($, inputs) }
      await update($, lastReview, () => ran)
      await pin($)
    }

    return next(e)
  })

  on('command.run', { command: 'wt' }, async ($, e) => {
    const word = e.args.trim().split(/\s+/)[0] ?? ''
    const known: readonly Action[] = knobs.hasGittree
      ? ['up', 'undo', 'push', 'resume', 'fetch', 'refresh', 'gittree']
      : ['up', 'undo', 'push', 'resume', 'fetch', 'refresh']
    const action = known.find(one => one === word)

    if (action !== undefined) {
      return { text: await act($, action) }
    }

    if (word !== '') {
      return { text: `/wt ${word}: not one of ${known.join(', ')}.` }
    }

    await update($, hiddenFor, () => null)
    const p = await refresh($)
    await $.ui.open({ id: PANE, title: 'wt' })

    return { text: statusText(p, await $.clock.now()) ?? 'No wt worktree here.' }
  })

  on('ui.render', { component: 'AbovePrompt' }, async ($, e, next) => {
    isTurnRunning = e.props.isWorking

    if (e.props.hasSurvey) {
      return next(e)
    }

    const p = await read($, plan)
    const last = await read($, outcome)
    const offer = offerOf(p, last, await read($, busy), await $.clock.now())
    const { Box, Button, Text } = $.ui.resolve(e)

    if (offer === null || (last === null && (await read($, hiddenFor)) === signature(p))) {
      // Nothing to do about the worktree. With the gittree knob the band
      // stays, one quiet row, so opening it there is always one key away.
      if (!knobs.hasGittree) {
        return next(e)
      }

      return (
        <Box gap={2}>
          <Button key="gittree" plain dimColor label={LABEL.gittree} hotkey={HOTKEY.gittree} onPress={() => act($, 'gittree')} />
          <Button key="details" plain dimColor label="wt details" hotkey={HOTKEY.details} onPress={() => act($, 'details')} />
        </Box>
      )
    }

    const offered = knobs.hasGittree && offer.actions.length > 0 ? [...offer.actions.filter(one => one !== 'hide'), 'gittree' as const, ...offer.actions.filter(one => one === 'hide')] : offer.actions
    const actions = e.props.isWorking ? offered.filter(one => !MOVES.includes(one)) : offered
    const isHeld = actions.length < offered.length

    return (
      <Box flexDirection="column">
        <Text color={offer.tone} wrap="wrap">
          {offer.text}
          {isHeld ? ' (wt can move it once this turn is done.)' : ''}
        </Text>
        <Box gap={1}>
          {actions.map(one => (
            <Button
              key={one}
              label={LABEL[one]}
              hotkey={HOTKEY[one]}
              variant={one === 'up' || one === 'resume' ? 'primary' : 'secondary'}
              onPress={() => act($, one)}
            />
          ))}
        </Box>
      </Box>
    )
  })

  on('ui.render', { component: 'Pane', requestId: PANE }, async ($, e) => {
    const { Box, Button, Text } = $.ui.resolve(e)
    const p = await read($, plan)
    const last = await read($, outcome)
    const state = await read($, busy)

    if (p === null) {
      return (
        <Box flexDirection="column">
          <Text dimColor wrap="wrap">
            No wt worktree here: this folder is not in a repository wt knows, or wt is not installed.
          </Text>
          <Button key="refresh" label={LABEL.refresh} onPress={() => act($, 'refresh')} />
        </Box>
      )
    }

    const now = await $.clock.now()
    const age = p.fetchedAt === null ? 'fetch time unknown' : `last fetched ${ago(now, p.fetchedAt)}`
    const why = obstacle(p)
    const isBehind = hasWork(p)
    // The last outcome, when it is this worktree's.
    const mine = last !== null && last.path === p.path ? last : null
    const actions: Action[] = ['refresh', 'fetch']

    if (p.upIneligibleCode === 'handedOver') {
      actions.push('resume', 'undo')
    } else if (canUp(p)) {
      actions.push('up')
    } else if (p.upIneligibleCode === 'ownRemoteDiverged' && p.token !== null) {
      actions.push('upDiverged')
    }

    if (mine !== null && mine.pushCommand !== null) {
      actions.push('push')
    }

    if (mine !== null && mine.canUndo && !actions.includes('undo')) {
      actions.push('undo')
    }

    if (knobs.hasGittree) {
      actions.push('gittree')
    }

    const review = knobs.hasReview ? await read($, lastReview) : null
    const typed = knobs.hasReview ? await read($, lastInput) : null
    const count = knobs.hasReview ? await read($, inputs) : 0

    const rows: Array<[string, string, Tone | undefined]> = [
      ['Branch', `${p.branch ?? 'detached'}${p.isMain ? ' (the main checkout)' : ''}`, undefined],
      [
        'Trunk',
        `${p.trunkRef}, ${age}${p.trunkRemoteAhead > 0 ? `; local ${p.trunk} is ${p.trunkRemoteAhead} behind it` : ''}`,
        undefined,
      ],
    ]

    if (!p.isMain) {
      rows.push([
        'Position',
        p.behind === null || p.ahead === null ? `could not be compared with ${p.trunkRef}` : `${p.behind} behind, ${p.ahead} ahead of ${p.trunkRef}`,
        p.behind === null ? 'warning' : p.behind > 0 ? 'warning' : 'success',
      ])
      rows.push(['Tree', p.tree === 'dirty' ? 'uncommitted changes (untracked files count)' : p.tree, p.tree === 'clean' ? undefined : 'warning'])

      const own = ownWords(p, false)

      rows.push([
        'Own remote',
        own ?? (p.ownRemote === null ? 'not reported by this wt' : 'nothing pushed under its name'),
        p.ownRemote?.state === 'diverged' ? 'error' : p.ownRemote?.state === 'behind' ? 'warning' : undefined,
      ])

      if (p.stack.length > 0) {
        rows.push(['Stack', `moves with ${p.stack.join(', ')}`, undefined])
      }

      if (p.sessionsError !== null) {
        rows.push(['Sessions', `could not be listed: ${p.sessionsError}`, 'warning'])
      } else if (p.sessions.length > 0) {
        rows.push(['Sessions', p.sessions.map(one => `${one.name} (${one.kind}, ${one.state})`).join(', '), undefined])
      }

      rows.push([
        'wt up',
        why !== undefined ? `would refuse: ${why}` : isBehind ? `ready: ${distance(p)}` : 'nothing to do, it is on trunk',
        why !== undefined ? 'warning' : isBehind ? 'suggestion' : 'success',
      ])
    }

    return (
      <Box flexDirection="column">
        <Text bold>{p.work}</Text>
        <Text dimColor wrap="truncate-middle">
          {p.path}
        </Text>
        <Box flexDirection="column" marginTop={1}>
          {rows.map(([name, value, tone]) => (
            <Box gap={1}>
              <Box width={11}>
                <Text dimColor>{name}</Text>
              </Box>
              <Text color={tone} wrap="wrap">
                {value}
              </Text>
            </Box>
          ))}
        </Box>
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
        {knobs.hasReview && (
          <Box flexDirection="column" marginTop={1}>
            <Box gap={1}>
              <Box width={11}>
                <Text dimColor>Review</Text>
              </Box>
              <Text color={review === null ? 'warning' : undefined} wrap="wrap">
                {review === null
                  ? 'no review skill has run in this conversation'
                  : `/${review.skill}, ${ago(now, review.at)}, ${inputsSince(count - review.inputs)}`}
              </Text>
            </Box>
            <Box gap={1}>
              <Box width={11}>
                <Text dimColor>Last input</Text>
              </Box>
              <Text wrap="wrap">
                {typed === null ? 'none yet' : `${ago(now, typed.at)}: “${typed.head}” (${count} so far)`}
              </Text>
            </Box>
          </Box>
        )}
        <Box gap={1} marginTop={1}>
          {state === 'idle' &&
            actions.map(one => (
              <Button
                key={one}
                label={LABEL[one]}
                variant={one === 'up' || one === 'resume' ? 'primary' : 'secondary'}
                onPress={() => act($, one)}
              />
            ))}
        </Box>
        <Box marginTop={1}>
          <Text dimColor wrap="wrap">
            wt up fetches, fast-forwards local {p.trunk} and a branch behind its own remote, rebases the whole
            stack with the repository's strategies and never pushes. It keeps a safety ref, so Undo puts the
            branch back. Push… puts the lease-protected push in your prompt, for you to run.
          </Text>
        </Box>
      </Box>
    )
  })
}
