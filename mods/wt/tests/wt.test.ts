import { expect, mock, test } from 'claude-code/testing'
import type { On } from 'claude-code'

const PATH = '/repos/app_wt/feat_wt/login'
const TOKEN = '1:0123abcd'

// A member of the stack as status lists it: status 1.7.0 names the deferred
// steps trunk declares, which the mod does not read.
const member = (work: string, path: string): Record<string, unknown> => ({
  work,
  branch: `feat_wt/${work}`,
  path,
  ownRemote: { ref: `origin/feat_wt/${work}`, state: 'inSync', commit: 'c', ahead: 0, behind: 0, fetched: false, blocks: null },
  deferredDeclared: [],
})

// `wt status --json` for a worktree 4 behind trunk, as wt 946b3c3 prints it.
const status = (over: Record<string, unknown> = {}, worktree: Record<string, unknown> = {}): string =>
  JSON.stringify({
    schema: 1,
    schemaVersion: '1.7.0',
    command: 'status',
    token: TOKEN,
    trunk: 'main',
    trunkRef: 'origin/main',
    trunkRefUpdatedAt: '2026-10-07T08:00:00Z',
    trunkSync: { local: 'a', remote: 'a', localAhead: 0, remoteAhead: 0, fastForwarded: false, skippedReason: null },
    worktree: {
      work: 'login',
      branch: 'feat_wt/login',
      path: PATH,
      isMain: false,
      state: 'clean',
      behind: 4,
      ahead: 2,
      ownRemote: { ref: 'origin/feat_wt/login', state: 'inSync', commit: 'c', ahead: 0, behind: 0, fetched: false, blocks: null },
      ...worktree,
    },
    upEligible: true,
    upIneligibleCode: null,
    upIneligibleReason: null,
    stack: [member('login', PATH)],
    sessions: [],
    sessionsError: null,
    ...over,
  })

const UP_RESULT = JSON.stringify({
  schema: 1,
  command: 'up',
  trunkRef: 'origin/main',
  outcome: 'done',
  error: null,
  worktrees: [
    {
      work: 'login',
      branch: 'feat_wt/login',
      path: PATH,
      result: 'rebased',
      reason: null,
      before: '1111111aaaa',
      after: '2222222bbbb',
      pushCommand: ['git', '-C', PATH, 'push', '--force-with-lease', '--force-if-includes', 'origin', 'refs/heads/feat_wt/login:refs/heads/feat_wt/login'],
      pushed: false,
      ownRemoteSync: { ref: 'origin/feat_wt/login', state: 'inSync', fastForwarded: false },
      recovery: null,
    },
  ],
})

const BAND = {
  plugin: 'wt',
  component: 'AbovePrompt',
  props: {
    hasSurvey: false,
    isWorking: false,
    maxRows: 12,
    bodyColumns: 100,
    scroll: { offset: 0, bodyRows: 12 },
    view: {},
  },
} as const

const PANE = {
  plugin: 'wt',
  component: 'Pane',
  requestId: 'wt',
  props: { title: 'wt', isFocused: false, bodyColumns: 80, placement: 'dock', scroll: { offset: 0, bodyRows: 40 }, view: {} },
} as const

const RUN = { origin: { kind: 'composer' }, presentation: { isFullscreen: true, columns: 160 } } as const
const START = Date.parse('2026-10-07T09:00:00Z')

// A run's result as wt prints it, for the participants given.
const result = (command: string, outcome: string, error: string | null, worktrees: Record<string, unknown>[]): string =>
  JSON.stringify({ schema: 1, command, trunkRef: 'origin/main', outcome, error, worktrees })

const participant = (over: Record<string, unknown>): Record<string, unknown> => ({
  work: 'login',
  branch: 'feat_wt/login',
  path: PATH,
  result: 'rebased',
  reason: null,
  before: '1111111aaaa',
  after: '2222222bbbb',
  pushCommand: null,
  pushed: false,
  ownRemoteSync: null,
  recovery: null,
  ...over,
})

// The world beneath the mod: wt, git and gittree answered from the test
// (`answers` by the command's first three words, wt up by default), and
// everything the mod shows or records taken without a surface.
// When the repository of the test last fetched, as FETCH_HEAD's time.
world.fetchedAt = 0

function world(on: On, statusJson: () => string, answers: Record<string, string> = {}, refs: () => string = () => 'refs') {
  const runs: string[][] = []
  const cwds: (string | undefined)[] = []
  const shown: {
    status: string | undefined
    toasts: string[]
    fills: string[]
    opened: { id: string; focus?: true; closeOnEscape?: true; rows?: number }[]
    closed: string[]
    isOpen: boolean
  } = { status: undefined, toasts: [], fills: [], opened: [], closed: [], isOpen: false }
  // What a test turns to make the world awkward: a wt run that does not end
  // until released, a prompt that takes no text, a pane open behind another.
  const rig: {
    hold: Promise<void> | null
    holdFetch: Promise<void> | null
    isFillRefused: boolean
    isPaneBehind: boolean
    seed: Record<string, unknown>
  } = { hold: null, holdFetch: null, isFillRefused: false, isPaneBehind: false, seed: {} }
  // The keys of its state the mod wrote, in order; and what a test seeds a
  // key with, as a session holds it from before.
  const writes: string[] = []
  on('state.set', ($, e, next) => {
    writes.push(e.key)

    return next(e)
  })
  on('state.get', ($, e, next) => (e.key in rig.seed ? { value: { value: rig.seed[e.key], version: 1 } } : next(e)))
  const clock = mock.clock(on, { now: START })

  on('process.run', async ($, e) => {
    runs.push([...e.argv])
    cwds.push(e.init?.cwd)
    const key = e.argv.slice(0, 3).join(' ')

    if (key === 'wt up --yes' && rig.hold !== null) {
      await rig.hold
    }

    if (e.argv[1] === 'fetch' && rig.holdFetch !== null) {
      await rig.holdFetch
    }

    const stdout =
      key === 'wt status --json'
        ? statusJson()
        : e.argv[1] === 'for-each-ref'
          ? refs()
          : (answers[key] ?? (key === 'wt up --yes' ? UP_RESULT : ''))

    return { value: { exitCode: 0, stdout, stderr: '', isStdoutTruncated: false, isStderrTruncated: false } }
  })
  on('ui.status', ($, e) => {
    shown.status = e.text

    return { value: undefined }
  })
  on('ui.toast', ($, e) => {
    shown.toasts.push(e.text)

    return { value: undefined }
  })
  on('prompt.fill', ($, e) => {
    if (rig.isFillRefused) {
      return { isFilled: false, refusal: 'dialog' }
    }

    shown.fills.push(e.text)

    return { isFilled: true }
  })
  on('ui.open', ($, e) => {
    shown.opened.push({ ...e })
    shown.isOpen = true

    return { value: { isPlaced: true } }
  })
  on('ui.close', ($, e) => {
    shown.closed.push(e.id)
    shown.isOpen = false

    return { value: undefined }
  })
  // What the engine draws where the mod draws nothing.
  on('ui.render', () => ({ type: 'Text', props: {}, children: ['the engine’s own'] }))
  on('ui.panes', () => ({
    value: shown.isOpen ? [{ id: 'wt', title: 'wt', isShown: !rig.isPaneBehind, isFocused: !rig.isPaneBehind, isPlaced: true }] : [],
  }))
  on('prompt.submit', ($, e) => ({ text: e.text }))
  on('skill.prompt', ($, e) => ({ text: e.text }))
  on('turn.start', ($, e) => ({ turnId: e.turnId }))
  on('session.start', ($, e) => ({ cwd: e.cwd }))
  on('session.end', () => ({ sessionId: 'test' }))
  on('fs.stat', () => ({ value: { kind: 'file', size: 1, mtimeMs: world.fetchedAt, isLink: false } }))
  on('command.register', ($, e) => ({ value: { command: e.name } }))

  return { runs, cwds, shown, clock, rig, writes }
}

type Mounted = { find(query: { type: 'Text' | 'Box'; text?: RegExp }): Promise<unknown> }
type Drawn = { type?: string; props?: { key?: string; color?: string; dimColor?: boolean; flexGrow?: number }; children?: unknown[] }

// The line above the prompt as "colour:text" for each part, in the order
// drawn: the line is one Text holding a Text for each part. 'none' is the
// default colour.
const partsOf = async (band: Mounted): Promise<string[]> => {
  const line = (await band.find({ type: 'Text', text: /./ })) as Drawn | undefined

  return (line?.children ?? [])
    .filter((child): child is Drawn => typeof child === 'object' && child !== null)
    .map(part => `${part.props?.color ?? (part.props?.dimColor === true ? 'dim' : 'none')}:${String(part.children?.[0])}`)
}

// What the row above the prompt holds, left to right.
const rowOf = async (band: Mounted): Promise<string[]> => {
  const row = (await band.find({ type: 'Box' })) as Drawn | undefined

  return (row?.children ?? [])
    .filter((child): child is Drawn => typeof child === 'object' && child !== null)
    .map(child => (child.type === 'Button' ? `Button ${child.props?.key}` : child.type === 'Box' && child.props?.flexGrow === 1 ? 'spacer' : String(child.type)))
}

// The colour of the part of the line that reads `text`: the line is one Text
// holding a Text for each part. 'none' for a part in the default colour.
const partColour = (line: unknown, text: string): unknown => {
  const children: unknown[] = (line as { children?: unknown[] } | undefined)?.children ?? []
  const part = children.find(child => typeof child === 'object' && child !== null && (child as { children?: unknown[] }).children?.[0] === text)

  expect(part).toBeDefined()

  return (part as { props?: { color?: unknown } }).props?.color ?? 'none'
}

// The branch's own remote as status reports it in the state given.
const ownRemote = (state: string): Record<string, unknown> => ({
  ownRemote: { ref: 'origin/feat_wt/login', state, commit: 'c', ahead: 2, behind: 2, fetched: false, blocks: null },
})

test('a worktree behind trunk gets one line that opens the dialog, and wt up there runs with the plan token', async ($, on) => {
  let own = 'inSync'
  const { runs, cwds, shown } = world(on, () => status({}, ownRemote(own)))

  expect((await $.command.run({ ...RUN, command: 'wt', args: 'refresh' })).text).toBe('login ↓4↑2 main')

  // The line above the prompt says it and opens the dialog; nothing on it
  // moves a branch.
  for (const surface of ['terminal', 'desktop'] as const) {
    const band = await $.ui.mount({ ...BAND, surface })
    expect(await band.find({ type: 'Text', text: '· login ↓4↑2 main' })).toBeDefined()
    expect(await band.find({ type: 'Button', key: 'up' })).toBeUndefined()
    // It takes no key: a digit typed into an empty prompt stays the person's.
    const opener = await band.find({ type: 'Button', key: 'details' })
    expect(opener?.props.label).toBe('/wt')
    expect(opener?.props.hotkey).toBeUndefined()
    await band.press({ key: 'details' })
    await band.unmount()
  }

  // The first click opened the dialog and the second, on it open, closed it.
  expect(shown.opened).toHaveLength(1)
  expect(shown.closed).toEqual(['wt'])

  const pane = await $.ui.mount({ ...PANE, surface: 'terminal' })
  own = 'rebased'
  await pane.press({ key: 'up' })

  expect(runs).toContainEqual(['wt', 'up', '--yes', '--no-push', '--json', '--expect', TOKEN])
  expect(await pane.find({ type: 'Text', text: /login rebased onto origin\/main \(1111111 → 2222222\)/ })).toBeDefined()
  expect(await pane.find({ type: 'Button', key: 'push' })).toBeDefined()
  expect(await pane.find({ type: 'Button', key: 'undo' })).toBeDefined()

  const band = await $.ui.mount({ ...BAND, surface: 'terminal' })
  expect(await band.find({ type: 'Text', text: '· ✓ login rebased onto origin/main · ↑ origin (rebased)' })).toBeDefined()
  await band.unmount()

  // Push runs nothing: the lease-protected push goes in the prompt.
  // The dialog closes first: the prompt takes nothing under one.
  await pane.press({ key: 'push' })
  expect(shown.closed).toEqual(['wt', 'wt'])
  expect(shown.fills).toEqual([`! git -C ${PATH} push --force-with-lease --force-if-includes origin refs/heads/feat_wt/login:refs/heads/feat_wt/login`])
  expect(shown.toasts.at(-1)).toBe('The push is in the prompt: press Enter to run it.')
  expect(runs.some(argv => argv[0] === 'git' && argv.includes('push'))).toBe(false)

  // Undo acts on the worktree the command runs in.
  await pane.press({ key: 'undo' })
  const undo = runs.findIndex(argv => argv[2] === 'undo')
  expect(runs[undo]).toEqual(['wt', 'sync', 'undo', '.', '--yes', '--json'])
  expect(cwds[undo]).toBe(PATH)
  await pane.unmount()
})

test('a result that succeeded goes once the branch is pushed, and one that failed stays', async ($, on) => {
  let own = 'inSync'
  let behind = 4
  const answers = { 'wt up --yes': UP_RESULT }
  world(on, () => status({}, { behind, ...ownRemote(own) }), answers)

  await $.command.run({ ...RUN, command: 'wt', args: 'refresh' })
  const band = await $.ui.mount({ ...BAND, surface: 'terminal' })
  own = 'rebased'
  behind = 0
  await $.command.run({ ...RUN, command: 'wt', args: 'up' })
  expect(await band.find({ type: 'Text', text: /✓ login rebased onto origin\/main/ })).toBeDefined()

  // Pushed from the prompt: nothing is left to do, and the line says how
  // the worktree stands again.
  own = 'inSync'
  await $.command.run({ ...RUN, command: 'wt', args: 'refresh' })
  expect(await band.find({ type: 'Text', text: '· login ✓↑2 main' })).toBeDefined()

  behind = 4
  answers['wt up --yes'] = result('up', 'refused', 'the plan changed since it was shown: look again', [])
  await $.command.run({ ...RUN, command: 'wt', args: 'up' })
  await $.command.run({ ...RUN, command: 'wt', args: 'refresh' })
  expect(await band.find({ type: 'Text', text: '· ✗ wt did not touch login' })).toBeDefined()
  await band.unmount()
})

test('a prompt that does not take the push is said, with the command to run', async ($, on) => {
  let own = 'inSync'
  const { shown, rig } = world(on, () => status({}, ownRemote(own)))

  await $.command.run({ ...RUN, command: 'wt', args: 'refresh' })
  own = 'rebased'
  await $.command.run({ ...RUN, command: 'wt', args: 'up' })
  rig.isFillRefused = true

  const said = `The prompt did not take the push. Run it yourself: ! git -C ${PATH} push --force-with-lease --force-if-includes origin refs/heads/feat_wt/login:refs/heads/feat_wt/login`
  expect((await $.command.run({ ...RUN, command: 'wt', args: 'push' })).text).toBe(said)
  expect(shown.toasts.at(-1)).toBe(said)
  expect(shown.fills).toEqual([])
})

test('while wt moves the branch nothing else fetches, from the dialog or from /wt', async ($, on) => {
  const { runs, rig } = world(on, () => status())
  let release = (): void => undefined

  await $.command.run({ ...RUN, command: 'wt', args: 'refresh' })
  rig.hold = new Promise<void>(resolve => {
    release = resolve
  })
  const run = $.command.run({ ...RUN, command: 'wt', args: 'up' })

  for (let turn = 0; turn < 1_000 && !runs.some(argv => argv[1] === 'up'); turn += 1) {
    await Promise.resolve()
  }

  expect(runs.some(argv => argv[1] === 'up')).toBe(true)
  const pane = await $.ui.mount({ ...PANE, surface: 'terminal' })
  expect(await pane.find({ type: 'Text', text: /wt up is running/ })).toBeDefined()
  expect(await pane.find({ type: 'Button', key: 'fetch' })).toBeUndefined()
  expect(await pane.find({ type: 'Button', key: 'refresh' })).toBeUndefined()
  expect(await pane.find({ type: 'Button', key: 'close' })).toBeDefined()
  await pane.unmount()
  expect((await $.command.run({ ...RUN, command: 'wt', args: 'fetch' })).text).toBe('wt is already running here.')
  expect(runs.some(argv => argv[0] === 'git' && argv[1] === 'fetch')).toBe(false)

  release()
  await run
  expect((await $.command.run({ ...RUN, command: 'wt', args: 'fetch' })).text).toBe('Fetched origin/main.')
})

test('wt up pressed while a fetch of the person’s runs waits for it', async ($, on) => {
  const { runs, rig } = world(on, () => status())
  let release = (): void => undefined
  const spin = async (until: () => boolean): Promise<void> => {
    for (let turn = 0; turn < 1_000 && !until(); turn += 1) {
      await Promise.resolve()
    }
  }

  await $.command.run({ ...RUN, command: 'wt', args: 'refresh' })
  rig.holdFetch = new Promise<void>(resolve => {
    release = resolve
  })
  const fetch = $.command.run({ ...RUN, command: 'wt', args: 'fetch' })
  await spin(() => runs.some(argv => argv[1] === 'fetch'))
  expect(runs.some(argv => argv[1] === 'fetch')).toBe(true)
  expect((await $.command.run({ ...RUN, command: 'wt', args: 'fetch' })).text).toBe('A fetch is already running here.')

  const up = $.command.run({ ...RUN, command: 'wt', args: 'up' })
  await spin(() => runs.some(argv => argv[1] === 'up'))
  expect(runs.some(argv => argv[1] === 'up')).toBe(false)

  release()
  await Promise.all([fetch, up])
  expect(runs.filter(argv => argv[1] === 'up')).toHaveLength(1)
})

test('a result with nothing to push goes when the person types on, and /wt dismiss clears any', async ($, on) => {
  const answers = { 'wt sync undo': result('sync undo', 'done', null, [participant({ result: 'undone' })]) }
  world(on, () => status(), answers)

  await $.command.run({ ...RUN, command: 'wt', args: 'refresh' })
  const band = await $.ui.mount({ ...BAND, surface: 'terminal' })
  await $.command.run({ ...RUN, command: 'wt', args: 'undo' })
  expect(await band.find({ type: 'Text', text: '· ✓ login is back where it was' })).toBeDefined()

  // A command of the mod's own is not typing on.
  await $.prompt.submit({ text: '/wt', origin: { kind: 'composer' }, wait: false })
  expect(await band.find({ type: 'Text', text: '· ✓ login is back where it was' })).toBeDefined()
  await $.prompt.submit({ text: 'right, on with it', origin: { kind: 'composer' }, wait: false })
  expect(await band.find({ type: 'Text', text: '· login ↓4↑2 main' })).toBeDefined()

  // A failure stays through that, and goes with /wt dismiss.
  answers['wt sync undo'] = result('sync undo', 'refused', 'feat_wt/login has moved since the run', [participant({ result: 'notRun' })])
  await $.command.run({ ...RUN, command: 'wt', args: 'undo' })
  await $.prompt.submit({ text: 'hm', origin: { kind: 'composer' }, wait: false })
  expect(await band.find({ type: 'Text', text: '· ✗ wt did not touch login' })).toBeDefined()
  expect((await $.command.run({ ...RUN, command: 'wt', args: 'dismiss' })).text).toBe('Dismissed.')
  expect(await band.find({ type: 'Text', text: '· login ↓4↑2 main' })).toBeDefined()
  await band.unmount()
})

// The worktree as wt reports one whose branch diverged from its own remote:
// not eligible, the reason given, and a token for the override all the same.
const DIVERGED = 'feat_wt/login has diverged from origin/feat_wt/login: 2 here, 1 there.'
const diverged = (worktree: Record<string, unknown> = {}): string =>
  status(
    { upEligible: false, upIneligibleCode: 'ownRemoteDiverged', upIneligibleReason: DIVERGED },
    { ownRemote: { ref: 'origin/feat_wt/login', state: 'diverged', commit: 'c', ahead: 2, behind: 1, fetched: false, blocks: 'diverged' }, ...worktree },
  )

test('the worktree is said in git’s marks, each part in the colour that says how it is', async ($, on) => {
  let json = status({}, { behind: 0, ahead: 0 })
  world(on, () => json)
  // What /wt refresh answers, and the line's parts with their colours.
  const said = async (): Promise<[string | undefined, string[]]> => {
    const text = (await $.command.run({ ...RUN, command: 'wt', args: 'refresh' })).text
    const band = await $.ui.mount({ ...BAND, surface: 'terminal' })
    const parts = await partsOf(band)
    await band.unmount()

    return [text, parts]
  }
  const own = (state: string, ahead: number, behind: number, blocks: string | null = null): Record<string, unknown> => ({
    ownRemote: { ref: 'origin/feat_wt/login', state, commit: 'c', ahead, behind, fetched: false, blocks },
  })

  // As it should be: no colour.
  expect(await said()).toEqual(['login ✓ main', ['none:login ✓ main']])
  json = status({}, { behind: 0, ahead: 2 })
  expect(await said()).toEqual(['login ✓↑2 main', ['none:login ✓↑2 main']])
  json = status({}, { behind: 0, ahead: 3, ...own('ahead', 3, 0) })
  expect(await said()).toEqual(['login ✓↑3 main · ↑3 origin', ['none:login ✓↑3 main', 'none:↑3 origin']])
  json = status({}, { behind: 0, ahead: 3, ...own('rebased', 3, 3) })
  expect(await said()).toEqual(['login ✓↑3 main · ↑ origin (rebased)', ['none:login ✓↑3 main', 'none:↑ origin (rebased)']])
  json = status({}, { behind: 0, ahead: 2, state: 'dirty' })
  expect(await said()).toEqual(['login ✓↑2 main *', ['none:login ✓↑2 main *']])

  // Something for wt up to do: the accent colour, on the part it is about.
  json = status()
  expect(await said()).toEqual(['login ↓4↑2 main', ['suggestion:login ↓4↑2 main']])
  json = status({}, { behind: 0, ahead: 0, ...own('behind', 0, 1) })
  expect(await said()).toEqual(['login ✓ main · ↓1 origin', ['none:login ✓ main', 'suggestion:↓1 origin']])
  json = status({}, { state: 'dirty', ...own('ahead', 3, 0) })
  expect(await said()).toEqual(['login ↓4↑2 main · ↑3 origin *', ['suggestion:login ↓4↑2 main', 'none:↑3 origin *']])

  // Wrong, or wt could not say: the warning colour, and no accent anywhere
  // while wt up would refuse.
  json = status({}, { behind: null, ahead: null })
  expect(await said()).toEqual(['login ? main', ['none:login ? main']])
  json = status({}, { behind: 0, ahead: 0, ...own('gone', 0, 0) })
  expect(await said()).toEqual(['login ✓ main · ✗ origin', ['none:login ✓ main', 'warning:✗ origin']])
  json = diverged()
  expect(await said()).toEqual([
    'login ↓4↑2 main · ↓1↑2 origin diverged · wt up would refuse',
    ['none:login ↓4↑2 main', 'warning:↓1↑2 origin diverged', 'warning:wt up would refuse'],
  ])
  json = status({ upEligible: false, upIneligibleCode: 'ownRemoteBehind', upIneligibleReason: 'it has uncommitted changes' }, { state: 'dirty', ...own('behind', 0, 3, 'dirty') })
  expect(await said()).toEqual([
    'login ↓4↑2 main · ↓3 origin * · wt up would refuse',
    ['none:login ↓4↑2 main', 'none:↓3 origin *', 'warning:wt up would refuse'],
  ])
  json = status({ sessions: [{ name: 'other', kind: 'claude', state: 'busy' }] })
  expect(await said()).toEqual(['login ↓4↑2 main · wt up would refuse', ['none:login ↓4↑2 main', 'warning:wt up would refuse']])
  json = status({ upEligible: false, upIneligibleCode: 'handedOver' }, { state: 'dirty' })
  expect(await said()).toEqual(['login ↓4↑2 main · REBASE STOPPED', ['none:login ↓4↑2 main', 'warning:REBASE STOPPED']])

  // The main checkout has no line; /wt refresh still says how it stands.
  json = status(
    { token: null, upEligible: false, upIneligibleCode: 'mainCheckout', trunkSync: { remoteAhead: 3 } },
    { work: 'app', branch: 'main', isMain: true, behind: 0, ahead: 0 },
  )
  expect((await said())[0]).toBe('main checkout ↓3 origin')
})

test('a result never hides what is wrong with the worktree as it stands now', async ($, on) => {
  let json = status()
  const answers = { 'wt up --yes': result('up', 'refused', 'the plan changed since it was shown: look again', []) }
  world(on, () => json, answers)
  const line = async (): Promise<string[]> => {
    await $.command.run({ ...RUN, command: 'wt', args: 'refresh' })
    const band = await $.ui.mount({ ...BAND, surface: 'terminal' })
    const parts = await partsOf(band)
    await band.unmount()

    return parts
  }

  await $.command.run({ ...RUN, command: 'wt', args: 'refresh' })
  await $.command.run({ ...RUN, command: 'wt', args: 'up' })
  expect(await line()).toEqual(['error:✗ wt did not touch login'])

  // The failure stays, and the worktree has since gone wrong in other ways.
  json = diverged()
  expect(await line()).toEqual(['error:✗ wt did not touch login', 'warning:↓1↑2 origin diverged', 'warning:wt up would refuse'])
  json = status({ upEligible: false, upIneligibleCode: 'handedOver' }, { state: 'dirty' })
  expect(await line()).toEqual(['error:✗ wt did not touch login', 'warning:REBASE STOPPED'])

  // A success says what is left to push, in the colour that is its own.
  await $.command.run({ ...RUN, command: 'wt', args: 'dismiss' })
  json = status()
  answers['wt up --yes'] = UP_RESULT
  await $.command.run({ ...RUN, command: 'wt', args: 'refresh' })
  json = status({}, { behind: 0, ...ownRemote('rebased') })
  await $.command.run({ ...RUN, command: 'wt', args: 'up' })
  expect(await line()).toEqual(['success:✓ login rebased onto origin/main', 'none:↑ origin (rebased)'])
  json = diverged({ behind: 0 })
  expect(await line()).toEqual(['success:✓ login rebased onto origin/main', 'warning:↓1↑2 origin diverged'])
})

test('while a turn runs nothing moves the branch, from the dialog or from /wt', async ($, on) => {
  const { runs, shown } = world(on, () => status())

  await $.command.run({ ...RUN, command: 'wt', args: 'refresh' })
  const band = await $.ui.mount({ ...BAND, surface: 'terminal', props: { ...BAND.props, isWorking: true } })
  const pane = await $.ui.mount({ ...PANE, surface: 'terminal' })

  await pane.press({ key: 'up' })
  expect(shown.toasts.at(-1)).toContain('wt can move it once the turn is done')
  expect((await $.command.run({ ...RUN, command: 'wt', args: 'up' })).text).toContain('a turn is running')
  expect(runs.some(argv => argv[1] === 'up')).toBe(false)
  await pane.unmount()
  await band.unmount()
})

test('a branch that diverged from its own remote is refused in wt’s words, with no wt up', async ($, on) => {
  const { runs } = world(on, () => diverged())

  expect((await $.command.run({ ...RUN, command: 'wt', args: 'refresh' })).text).toBe('login ↓4↑2 main · ↓1↑2 origin diverged · wt up would refuse')

  // The dialog says why and offers the override alone, which passes the flag
  // with the token.
  const pane = await $.ui.mount({ ...PANE, surface: 'terminal' })
  expect(await pane.find({ type: 'Text', text: `wt up would refuse: ${DIVERGED}` })).toBeDefined()
  expect(await pane.find({ type: 'Button', key: 'up' })).toBeUndefined()
  await pane.press({ key: 'upDiverged' })
  expect(runs).toContainEqual(['wt', 'up', '--yes', '--no-push', '--json', '--expect', TOKEN, '--allow-diverged'])
  await pane.unmount()

  // The override is a key in the dialog and never a word typed after /wt:
  // what /wt up runs there is wt's to refuse.
  const before = runs.length
  expect((await $.command.run({ ...RUN, command: 'wt', args: 'upDiverged' })).text).toContain('not one of')
  await $.command.run({ ...RUN, command: 'wt', args: 'up' })
  expect(runs.slice(before).filter(argv => argv[1] === 'up')).toEqual([['wt', 'up', '--yes', '--no-push', '--json', '--expect', TOKEN]])
})

test('a branch with nowhere to push is said in wt’s words, with the command wt names', async ($, on) => {
  const reason = 'it tracks origin/login, a branch of another name, and nothing says whether it pushes there or to origin/feat_wt/login'
  const fixCommand = ['wt', 'sync', 'push-to', 'feat_wt/login', 'origin/login']
  const unknown = { ref: null, state: 'unknown', commit: null, ahead: null, behind: null, fetched: false, blocks: null }
  let own: Record<string, unknown> = { ...unknown, noPushReason: reason, fixCommand }
  world(on, () => status({}, { ownRemote: own }))
  // The line about pushing in the dialog, and its colour; undefined when there is none.
  const said = async (): Promise<{ text: unknown; colour: unknown } | undefined> => {
    await $.command.run({ ...RUN, command: 'wt', args: 'refresh' })
    const pane = await $.ui.mount({ ...PANE, surface: 'terminal' })
    const note = await pane.find({ type: 'Text', text: /nowhere to push/ })
    await pane.unmount()

    return note === undefined ? undefined : { text: note.children?.[0], colour: note.props.color }
  }

  expect(await said()).toEqual({ text: `nowhere to push: ${reason}. wt sync push-to feat_wt/login origin/login records the first`, colour: 'warning' })

  // A reason no command settles is said alone.
  own = { ...unknown, noPushReason: 'it is recorded as pushing to nowhere/x, and there is no remote nowhere', fixCommand: null }
  expect((await said())?.text).toBe('nowhere to push: it is recorded as pushing to nowhere/x, and there is no remote nowhere')

  // A reason wt gives for a branch whose remote it names is said too, in place of the state's words.
  own = { ...unknown, ref: 'origin/login', state: 'diverged', ahead: 1, behind: 1, noPushReason: 'origin/login has commits this branch never had', fixCommand: null }
  expect((await said())?.text).toBe('nowhere to push: origin/login has commits this branch never had')

  // An unknown with no reason, as an older wt reports it, says nothing about pushing.
  own = { ...unknown }
  expect(await said()).toBeUndefined()
  own = { ...unknown, noPushReason: null, fixCommand: null }
  expect(await said()).toBeUndefined()
})

test('the push for a branch that pushes to another name goes in the prompt as wt spelled it', async ($, on) => {
  const pushCommand = ['git', '-C', PATH, 'push', '--force-with-lease=refs/heads/login:1111111aaaa', 'origin', 'refs/heads/feat_wt/login:refs/heads/login']
  // Rebased and not pushed yet, as status reads it against the recorded branch.
  const own = { ref: 'origin/login', state: 'rebased', commit: 'c', ahead: 2, behind: 2, fetched: false, blocks: null, noPushReason: null, fixCommand: null }
  const { shown } = world(on, () => status({}, { ownRemote: own }), { 'wt up --yes': result('up', 'done', null, [participant({ pushCommand })]) })

  await $.command.run({ ...RUN, command: 'wt', args: 'refresh' })
  const pane = await $.ui.mount({ ...PANE, surface: 'terminal' })
  await pane.press({ key: 'up' })
  await pane.press({ key: 'push' })
  await pane.unmount()

  expect(shown.fills).toEqual([`! git -C ${PATH} push --force-with-lease=refs/heads/login:1111111aaaa origin refs/heads/feat_wt/login:refs/heads/login`])
})

test('the dialog says wt up has nothing to do only when wt counted and found nothing', async ($, on) => {
  let json = status({}, { behind: 0 })
  world(on, () => json)
  const isSaid = async (): Promise<boolean> => {
    await $.command.run({ ...RUN, command: 'wt', args: 'refresh' })
    const pane = await $.ui.mount({ ...PANE, surface: 'terminal' })
    const note = await pane.find({ type: 'Text', text: 'not behind: wt up has nothing to do' })
    await pane.unmount()

    return note !== undefined
  }

  expect(await isSaid()).toBe(true)
  // Not counted is not none.
  json = status({}, { behind: null, ahead: null })
  expect(await isSaid()).toBe(false)
  json = status()
  expect(await isSaid()).toBe(false)
  // Diverged and not behind: the dialog offers the override instead.
  json = diverged({ behind: 0 })
  expect(await isSaid()).toBe(false)
})

test('the knobs are off unless asked for', async ($, on) => {
  const { runs } = world(on, () => status({}, { behind: 0 }))

  await $.command.run({ ...RUN, command: 'wt', args: 'refresh' })
  await $.prompt.submit({ text: 'hello there', origin: { kind: 'composer' }, wait: false })
  expect((await $.command.run({ ...RUN, command: 'wt', args: 'gittree' })).text).toContain('not one of')

  // Nothing to do: the line says how the worktree stands, and nothing else.
  const band = await $.ui.mount({ ...BAND, surface: 'terminal' })
  expect(await band.find({ type: 'Text', text: '· login ✓↑2 main' })).toBeDefined()
  expect(await band.find({ type: 'Button', key: 'details' })).toBeDefined()
  expect(await band.find({ type: 'Button', key: 'gittree' })).toBeUndefined()
  await band.unmount()

  // The dialog says why it has no key for wt up.
  const pane = await $.ui.mount({ ...PANE, surface: 'terminal' })
  expect(await pane.find({ type: 'Text', text: 'not behind: wt up has nothing to do' })).toBeDefined()
  expect(await pane.find({ type: 'Button', key: 'gittree' })).toBeUndefined()
  expect(await pane.find({ type: 'Text', text: 'last input' })).toBeUndefined()
  expect(runs.some(argv => argv[0] === 'gittree')).toBe(false)
  await pane.unmount()
})

test(
  'with the knobs on: gittree opens the worktree, and the last review and input are kept',
  { options: { gittree: true, reviewStatus: true } },
  async ($, on) => {
    const { runs, shown, clock } = world(on, () => status())

    await $.command.run({ ...RUN, command: 'wt', args: 'refresh' })

    await $.prompt.submit({ text: '/tc:pr-review   please look at the diff', origin: { kind: 'composer' }, wait: false })
    await $.skill.prompt({ skill: 'tc:pr-review', text: 'Review the PR.' })
    await $.skill.prompt({ skill: 'tc:commit', text: 'Commit.' })

    await clock.advance(42 * 60_000)
    await $.prompt.submit({ text: 'another session says hi', origin: { kind: 'peer' }, wait: false })
    await $.prompt.submit({ text: 'now fix the two findings from it', origin: { kind: 'composer' }, wait: false })
    await $.prompt.submit({ text: 'and push', origin: { kind: 'bridge' }, wait: false })

    // Above the prompt: the review is old enough to say, the input is not.
    const band = await $.ui.mount({ ...BAND, surface: 'terminal' })
    expect(await partsOf(band)).toEqual(['suggestion:login ↓4↑2 main', 'dim:reviewed 42 min ago, 2 inputs since'])
    await band.unmount()

    for (const surface of ['terminal', 'desktop'] as const) {
      const pane = await $.ui.mount({ ...PANE, surface })
      expect(await pane.find({ type: 'Text', text: 'last input' })).toBeDefined()
      expect(await pane.find({ type: 'Text', text: '/tc:pr-review, 42 min ago, 2 inputs since' })).toBeDefined()
      expect(await pane.find({ type: 'Text', text: 'just now: “and push” (3 so far)' })).toBeDefined()
      await pane.press({ key: 'gittree' })
      await pane.unmount()
    }

    expect(runs).toContainEqual(['gittree', PATH])
    expect(shown.toasts.at(-1)).toBe('Opened login in gittree.')
  },
)

test('above the prompt the last input shows from five minutes old and the review from thirty', { options: { reviewStatus: true } }, async ($, on) => {
  const { clock } = world(on, () => status({}, { behind: 0 }))
  const line = async (): Promise<string[]> => {
    const band = await $.ui.mount({ ...BAND, surface: 'terminal' })
    const parts = await partsOf(band)
    await band.unmount()

    return parts
  }

  await $.command.run({ ...RUN, command: 'wt', args: 'refresh' })
  await $.prompt.submit({ text: 'now fix the two findings from the review', origin: { kind: 'composer' }, wait: false })
  expect(await line()).toEqual(['none:login ✓↑2 main'])

  // Not a second before the limit, and from the limit on.
  await clock.advance(5 * 60_000 - 1_000)
  expect(await line()).toEqual(['none:login ✓↑2 main'])
  await clock.advance(1_000)
  expect(await line()).toEqual(['none:login ✓↑2 main', 'dim:last input 5 min ago: “now fix the two find…”'])

  // The same for a conversation no review has run in: thirty minutes in.
  await clock.advance(25 * 60_000 - 1_000)
  expect(await line()).toEqual(['none:login ✓↑2 main', 'dim:last input 30 min ago: “now fix the two find…”'])
  await clock.advance(1_000)
  expect(await line()).toEqual(['none:login ✓↑2 main', 'dim:last input 30 min ago: “now fix the two find…” · not reviewed yet, 1 input'])

  // A review, then typing on: both are fresh, and the line says neither.
  await $.skill.prompt({ skill: 'tc:pr-review', text: 'Review the PR.' })
  await $.prompt.submit({ text: 'thanks', origin: { kind: 'composer' }, wait: false })
  expect(await line()).toEqual(['none:login ✓↑2 main'])

  await clock.advance(30 * 60_000)
  expect(await line()).toEqual(['none:login ✓↑2 main', 'dim:last input 30 min ago: “thanks” · reviewed 30 min ago, 1 input since'])
})

test('the limits are options, and behind trunk the line says both', { options: { reviewStatus: true, inputAfterMinutes: 1, reviewAfterMinutes: 2 } }, async ($, on) => {
  const { clock } = world(on, () => status())

  await $.command.run({ ...RUN, command: 'wt', args: 'refresh' })
  await $.prompt.submit({ text: 'and push', origin: { kind: 'composer' }, wait: false })
  await clock.advance(60_000)

  let band = await $.ui.mount({ ...BAND, surface: 'terminal' })
  expect(await partsOf(band)).toEqual(['suggestion:login ↓4↑2 main', 'dim:last input 1 min ago: “and push”'])
  await band.unmount()

  await clock.advance(60_000)
  band = await $.ui.mount({ ...BAND, surface: 'terminal' })
  expect(await partsOf(band)).toEqual(['suggestion:login ↓4↑2 main', 'dim:last input 2 min ago: “and push” · not reviewed yet, 1 input'])
  expect(await rowOf(band)).toEqual(['Button details', 'Text', 'spacer'])
  await band.unmount()
})

test('a limit that is not a number of minutes from none up is the default', { options: { reviewStatus: true, inputAfterMinutes: -3, reviewAfterMinutes: -1 } }, async ($, on) => {
  const { clock } = world(on, () => status({}, { behind: 0 }))

  await $.command.run({ ...RUN, command: 'wt', args: 'refresh' })
  await $.prompt.submit({ text: 'and push', origin: { kind: 'composer' }, wait: false })
  await clock.advance(4 * 60_000)

  let band = await $.ui.mount({ ...BAND, surface: 'terminal' })
  expect(await partsOf(band)).toEqual(['none:login ✓↑2 main'])
  await band.unmount()

  await clock.advance(26 * 60_000)
  band = await $.ui.mount({ ...BAND, surface: 'terminal' })
  expect(await partsOf(band)).toEqual(['none:login ✓↑2 main', 'dim:last input 30 min ago: “and push” · not reviewed yet, 1 input'])
  await band.unmount()
})

test('a cleared conversation starts over: no input, no review, and no first input behind it', { options: { reviewStatus: true } }, async ($, on) => {
  const { clock } = world(on, () => status({}, { behind: 0 }))

  await $.command.run({ ...RUN, command: 'wt', args: 'refresh' })
  await $.prompt.submit({ text: 'and push', origin: { kind: 'composer' }, wait: false })
  await clock.advance(40 * 60_000)
  let band = await $.ui.mount({ ...BAND, surface: 'terminal' })
  expect(await partsOf(band)).toEqual(['none:login ✓↑2 main', 'dim:last input 40 min ago: “and push” · not reviewed yet, 1 input'])
  await band.unmount()

  await $.session.end({ reason: 'clear', sessionId: 'test', resume: { id: 'test' } })
  band = await $.ui.mount({ ...BAND, surface: 'terminal' })
  expect(await partsOf(band)).toEqual(['none:login ✓↑2 main'])
  await band.unmount()

  // The new conversation is as old as its own first input, not the old one's.
  await $.prompt.submit({ text: 'something else', origin: { kind: 'composer' }, wait: false })
  await clock.advance(6 * 60_000)
  band = await $.ui.mount({ ...BAND, surface: 'terminal' })
  expect(await partsOf(band)).toEqual(['none:login ✓↑2 main', 'dim:last input 6 min ago: “something else”'])
  await band.unmount()
})

test('a line already on screen says a minute more after a minute', { options: { reviewStatus: true } }, async ($, on) => {
  const { clock } = world(on, () => status({}, { behind: 0 }))

  await $.session.start({ cwd: PATH, surface: 'terminal', isInteractive: true })
  await clock.settle()
  await $.prompt.submit({ text: 'and push', origin: { kind: 'composer' }, wait: false })
  await clock.advance(5 * 60_000)

  const band = await $.ui.mount({ ...BAND, surface: 'terminal' })
  expect(await partsOf(band)).toEqual(['none:login ✓↑2 main', 'dim:last input 5 min ago: “and push”'])
  await clock.advance(60_000)
  expect(await partsOf(band)).toEqual(['none:login ✓↑2 main', 'dim:last input 6 min ago: “and push”'])
  await band.unmount()
})

test('without the review option nothing is kept of the conversation', async ($, on) => {
  const { clock, writes } = world(on, () => status({}, { behind: 0 }))

  await $.command.run({ ...RUN, command: 'wt', args: 'refresh' })
  await $.prompt.submit({ text: 'hello there', origin: { kind: 'composer' }, wait: false })
  await $.skill.prompt({ skill: 'tc:pr-review', text: 'Review the PR.' })
  await clock.advance(3 * 60 * 60_000)

  expect(writes.filter(key => ['inputs', 'lastInput', 'firstInput', 'lastReview'].includes(key))).toEqual([])
})

test('without the review option nothing kept from when it was on shows above the prompt', async ($, on) => {
  const { rig } = world(on, () => status({}, { behind: 0 }))

  // What a session holds from before the option was turned off.
  rig.seed = {
    inputs: 9,
    lastInput: { at: START - 42 * 60_000, head: 'now fix the two find…' },
    firstInput: START - 4 * 60 * 60_000,
    lastReview: { skill: 'tc:pr-review', at: START - 3 * 60 * 60_000, inputs: 4 },
  }
  await $.command.run({ ...RUN, command: 'wt', args: 'refresh' })

  const band = await $.ui.mount({ ...BAND, surface: 'terminal' })
  expect(await partsOf(band)).toEqual(['none:login ✓↑2 main'])
  await band.unmount()

  const pane = await $.ui.mount({ ...PANE, surface: 'terminal' })
  expect(await pane.find({ type: 'Text', text: 'last input' })).toBeUndefined()
  await pane.unmount()
})

test('a turn that started holds the moves where no band is drawn, and a second press does not start a second run', async ($, on) => {
  const { runs } = world(on, () => status())

  await $.command.run({ ...RUN, command: 'wt', args: 'refresh' })
  await $.turn.start({ text: 'go', turnId: 't1' })
  const held = await $.ui.mount({ ...PANE, surface: 'mobile' })
  await held.press({ key: 'up' })
  expect(runs.some(argv => argv[1] === 'up')).toBe(false)
  await held.unmount()

  // The band, drawn idle, says the turn is over: two presses, one run.
  const band = await $.ui.mount({ ...BAND, surface: 'terminal' })
  const pane = await $.ui.mount({ ...PANE, surface: 'terminal' })
  await Promise.all([pane.press({ key: 'up' }), $.command.run({ ...RUN, command: 'wt', args: 'up' })])
  expect(runs.filter(argv => argv[1] === 'up')).toHaveLength(1)
  await pane.unmount()
  await band.unmount()
})

test('a run wt refused says why, in red, and offers no undo', async ($, on) => {
  const refusal = 'the plan changed since it was shown: look again'
  world(on, () => status(), {
    'wt up --yes': result('up', 'refused', refusal, []),
    'wt sync undo': result('sync undo', 'refused', 'feat_wt/login has moved since the run', [participant({ result: 'notRun' })]),
  })

  await $.command.run({ ...RUN, command: 'wt', args: 'refresh' })
  expect((await $.command.run({ ...RUN, command: 'wt', args: 'up' })).text).toBe(`wt did not touch login: ${refusal}`)

  // In the error colour, on the line, and nowhere else.
  const band = await $.ui.mount({ ...BAND, surface: 'terminal' })
  expect(partColour(await band.find({ type: 'Text', text: '✗ wt did not touch login' }), '✗ wt did not touch login')).toBe('error')
  await band.unmount()

  const pane = await $.ui.mount({ ...PANE, surface: 'terminal' })
  expect(await pane.find({ type: 'Text', text: `wt did not touch login: ${refusal}` })).toBeDefined()
  expect(await pane.find({ type: 'Button', key: 'undo' })).toBeUndefined()
  expect(await pane.find({ type: 'Button', key: 'push' })).toBeUndefined()
  // Still behind and still eligible: the way on is to try again.
  expect(await pane.find({ type: 'Button', key: 'up' })).toBeDefined()
  await pane.unmount()

  expect((await $.command.run({ ...RUN, command: 'wt', args: 'undo' })).text).toBe('wt did not touch login: feat_wt/login has moved since the run')
})

test('a stack that did not settle is not a success and offers no push', async ($, on) => {
  world(on, () => status({ stack: [member('login', PATH), member('login-ui', `${PATH}-ui`)] }), {
    'wt up --yes': result('up', 'partial', null, [
      participant({ pushCommand: ['git', '-C', PATH, 'push', 'origin', 'feat_wt/login'] }),
      participant({ work: 'login-ui', path: `${PATH}-ui`, result: 'handedOver', reason: 'conflict in app.ts' }),
    ]),
  })

  await $.command.run({ ...RUN, command: 'wt', args: 'refresh' })
  const pane = await $.ui.mount({ ...PANE, surface: 'terminal' })
  expect(await pane.find({ type: 'Text', text: 'moves with login-ui' })).toBeDefined()
  expect(await pane.find({ type: 'Text', text: /not behind/ })).toBeUndefined()
  await pane.press({ key: 'up' })
  expect(await pane.find({ type: 'Text', text: /But in its stack login-ui stopped at a conflict: conflict in app.ts/ })).toBeDefined()
  expect(await pane.find({ type: 'Button', key: 'push' })).toBeUndefined()
  expect(await pane.find({ type: 'Button', key: 'undo' })).toBeDefined()

  const band = await $.ui.mount({ ...BAND, surface: 'terminal' })
  expect(await band.find({ type: 'Text', text: '· ✗ login rebased onto origin/main, but its stack did not settle' })).toBeDefined()

  // Dismissing the outcome brings back what is still to do.
  await pane.press({ key: 'dismiss' })
  expect(await band.find({ type: 'Text', text: '· login ↓4↑2 main' })).toBeDefined()
  await band.unmount()
  await pane.unmount()
})

test('the main checkout and a folder wt does not know stay quiet', async ($, on) => {
  let json = status({ token: null, upEligible: false, upIneligibleCode: 'mainCheckout' }, { work: 'app', branch: 'main', isMain: true, behind: 0, ahead: 0 })
  world(on, () => json)
  // Whether the mod drew nothing, leaving the row to whatever is beneath it.
  const isQuiet = async (): Promise<boolean> => {
    const band = await $.ui.mount({ ...BAND, surface: 'terminal' })
    const beneath = await band.find({ type: 'Text', text: 'the engine’s own' })
    expect(await band.find({ type: 'Button', key: 'details' })).toBeUndefined()
    await band.unmount()

    return beneath !== undefined
  }

  expect((await $.command.run({ ...RUN, command: 'wt', args: 'refresh' })).text).toBe('main checkout')
  expect((await $.command.run({ ...RUN, command: 'wt', args: 'up' })).text).toContain('wt up would refuse app')
  expect(await isQuiet()).toBe(true)

  json = 'wt: not inside a git repository'
  expect((await $.command.run({ ...RUN, command: 'wt', args: 'refresh' })).text).toBe('No wt worktree here.')
  expect(await isQuiet()).toBe(true)
})

test('a notice an older load of the mod left under the prompt is taken away', async ($, on) => {
  const { shown, clock } = world(on, () => status())

  shown.status = 'login · 4 behind main'
  await $.session.start({ cwd: PATH, surface: 'terminal', isInteractive: true })
  await clock.settle()
  expect(shown.status).toBeUndefined()
})

test('what another terminal did shows within one pulse, and a quiet repository costs no wt status', async ($, on) => {
  let behind = 4
  let tips = 'refs/remotes/origin/main aaa'
  const { runs, clock } = world(on, () => status({}, { behind }), {}, () => tips)
  const statusRuns = (): number => runs.filter(argv => argv[0] === 'wt' && argv[1] === 'status').length

  await $.session.start({ cwd: PATH, surface: 'terminal', isInteractive: true })
  await clock.settle()
  const band = await $.ui.mount({ ...BAND, surface: 'terminal' })
  expect(await band.find({ type: 'Text', text: '· login ↓4↑2 main' })).toBeDefined()

  // Nothing moved: the pulses read the refs and leave wt alone.
  await clock.advance(30_000)
  const quiet = statusRuns()
  await clock.advance(30_000)
  expect(statusRuns()).toBe(quiet)
  expect(runs.filter(argv => argv[1] === 'for-each-ref').at(-1)).toEqual([
    'git',
    'for-each-ref',
    '--format=%(refname) %(objectname)',
    'refs/heads/main',
    'refs/remotes/origin/main',
    'refs/heads/feat_wt/login',
    'refs/remotes/origin/feat_wt/login',
  ])

  // Someone rebased it from another terminal: one pulse later it shows here.
  behind = 0
  tips = 'refs/remotes/origin/main aaa\nrefs/heads/feat_wt/login bbb'
  await clock.advance(10_000)
  expect(await band.find({ type: 'Text', text: '· login ✓↑2 main' })).toBeDefined()
  await band.unmount()
})

test('nothing fetches by itself: an old fetch is said above the prompt, dim and after what is wrong', async ($, on) => {
  world.fetchedAt = START - 3 * 60 * 60_000
  let json = status()
  const { runs, clock } = world(on, () => json, { 'git rev-parse --path-format=absolute': '/repos/app/.git\n' })

  expect((await $.command.run({ ...RUN, command: 'wt', args: 'refresh' })).text).toBe('login ↓4↑2 main · fetched 3h ago')
  let band = await $.ui.mount({ ...BAND, surface: 'terminal' })
  expect(await partsOf(band)).toEqual(['suggestion:login ↓4↑2 main', 'dim:fetched 3h ago'])
  await band.unmount()

  // What is wrong comes before it, so a narrow terminal cuts the dim part first.
  json = diverged()
  await $.command.run({ ...RUN, command: 'wt', args: 'refresh' })
  band = await $.ui.mount({ ...BAND, surface: 'terminal' })
  expect(await partsOf(band)).toEqual(['none:login ↓4↑2 main', 'warning:↓1↑2 origin diverged', 'warning:wt up would refuse', 'dim:fetched 3h ago'])
  await band.unmount()

  // A recent fetch needs no words.
  world.fetchedAt = START - 5 * 60_000
  json = status()
  expect((await $.command.run({ ...RUN, command: 'wt', args: 'refresh' })).text).toBe('login ↓4↑2 main')

  // Hours of timers, each reading the worktree again, and never a fetch.
  await $.session.start({ cwd: PATH, surface: 'terminal', isInteractive: true })
  const reads = runs.filter(argv => argv[0] === 'wt' && argv[1] === 'status').length
  await clock.advance(2 * 60 * 60_000)
  expect(runs.filter(argv => argv[0] === 'wt' && argv[1] === 'status').length).toBeGreaterThan(reads + 30)
  expect(runs.some(argv => argv.includes('fetch'))).toBe(false)
  world.fetchedAt = 0
})

test('with the gittree knob gittree is the last thing on the row, after a spacer, with or without a line', { options: { gittree: true } }, async ($, on) => {
  let json = status({}, { behind: 0 })
  const { runs } = world(on, () => json)

  await $.command.run({ ...RUN, command: 'wt', args: 'refresh' })

  for (const surface of ['terminal', 'desktop'] as const) {
    const band = await $.ui.mount({ ...BAND, surface })
    expect(await rowOf(band)).toEqual(['Button details', 'Text', 'spacer', 'Button gittree'])
    expect(await partsOf(band)).toEqual(['none:login ✓↑2 main'])
    expect((await band.find({ type: 'Button', key: 'gittree' }))?.props.hotkey).toBeUndefined()
    await band.press({ key: 'gittree' })
    await band.unmount()
  }

  expect(runs.filter(argv => argv[0] === 'gittree')).toEqual([
    ['gittree', PATH],
    ['gittree', PATH],
  ])

  // The main checkout has no line of wt's: gittree alone, still at the end.
  json = status({ token: null, upEligible: false, upIneligibleCode: 'mainCheckout' }, { work: 'app', branch: 'main', path: '/repos/app', isMain: true, behind: 0, ahead: 0 })
  await $.command.run({ ...RUN, command: 'wt', args: 'refresh' })
  const band = await $.ui.mount({ ...BAND, surface: 'terminal' })
  expect(await rowOf(band)).toEqual(['Text', 'spacer', 'Button gittree'])
  expect((await band.find({ type: 'Button', key: 'gittree' }))?.props.label).toBe('gittree ↗')
  await band.press({ key: 'gittree' })
  await band.unmount()
  expect(runs.at(-1)).toEqual(['gittree', '/repos/app'])
})

test('the dialog opens with the keyboard, says how it closes, and its close key, /wt close and /wt again close it', async ($, on) => {
  const { shown, rig } = world(on, () => status())

  await $.command.run({ ...RUN, command: 'wt', args: '' })
  expect(shown.opened).toHaveLength(1)
  expect(shown.opened[0]?.id).toBe('wt')
  expect(shown.opened[0]?.focus).toBe(true)
  expect(shown.opened[0]?.closeOnEscape).toBe(true)

  for (const surface of ['terminal', 'desktop'] as const) {
    const pane = await $.ui.mount({ ...PANE, surface, props: { ...PANE.props, isFocused: true } })
    expect(await pane.find({ type: 'Text', text: 'Esc closes too' })).toBeDefined()
    await pane.press({ key: 'close' })
    await pane.unmount()
  }

  // Open without the keyboard, it says how to give it the keys.
  const keyless = await $.ui.mount({ ...PANE, surface: 'terminal' })
  expect(await keyless.find({ type: 'Text', text: 'click here, or ctrl+x then tab, for the keys' })).toBeDefined()
  expect(await keyless.find({ type: 'Text', text: 'Esc closes too' })).toBeUndefined()
  await keyless.unmount()

  expect(shown.closed).toEqual(['wt', 'wt'])
  expect((await $.command.run({ ...RUN, command: 'wt', args: 'close' })).text).toBe('wt pane closed.')

  // Bare /wt toggles: on show, it closes; closed, it opens.
  await $.command.run({ ...RUN, command: 'wt', args: '' })
  expect(shown.opened).toHaveLength(2)
  expect((await $.command.run({ ...RUN, command: 'wt', args: '' })).text).toBe('wt pane closed.')
  expect(shown.opened).toHaveLength(2)
  expect(shown.closed).toHaveLength(4)

  // Open behind another pane, it is raised rather than closed.
  await $.command.run({ ...RUN, command: 'wt', args: '' })
  rig.isPaneBehind = true
  expect((await $.command.run({ ...RUN, command: 'wt', args: '' })).text).toBe('login ↓4↑2 main')
  expect(shown.opened).toHaveLength(4)
  expect(shown.closed).toHaveLength(4)
})

test('a run from an open dialog asks again for the rows its result needs', async ($, on) => {
  let own = 'inSync'
  const { shown } = world(on, () => status({}, ownRemote(own)))

  await $.command.run({ ...RUN, command: 'wt', args: '' })
  const pane = await $.ui.mount({ ...PANE, surface: 'terminal' })
  own = 'rebased'
  await pane.press({ key: 'up' })
  await pane.unmount()

  expect(shown.opened).toHaveLength(2)
  expect(shown.opened[1]?.rows ?? 0).toBeGreaterThan(shown.opened[0]?.rows ?? 0)
  // The rows alone: the keyboard is not taken from a prompt he went back to.
  expect(shown.opened[0]?.focus).toBe(true)
  expect(shown.opened[1]?.focus).toBeUndefined()
  expect(shown.opened[1]?.closeOnEscape).toBe(true)
})

test('a prompt sent while wt moves the branch waits for the move', async ($, on) => {
  const { runs, rig } = world(on, () => status())
  let release = (): void => undefined
  let isSent = false

  await $.command.run({ ...RUN, command: 'wt', args: 'refresh' })
  rig.hold = new Promise<void>(resolve => {
    release = resolve
  })
  const run = $.command.run({ ...RUN, command: 'wt', args: 'up' })

  for (let turn = 0; turn < 1_000 && !runs.some(argv => argv[1] === 'up'); turn += 1) {
    await Promise.resolve()
  }

  const sent = $.prompt.submit({ text: 'and now the next thing', origin: { kind: 'composer' }, wait: false }).then(() => {
    isSent = true
  })

  for (let turn = 0; turn < 1_000; turn += 1) {
    await Promise.resolve()
  }

  expect(isSent).toBe(false)
  release()
  await Promise.all([run, sent])
  expect(isSent).toBe(true)
})

test('a result is shown and acted on only in the worktree it was run on', async ($, on) => {
  let json = status({}, ownRemote('inSync'))
  const { runs, shown } = world(on, () => json)

  await $.command.run({ ...RUN, command: 'wt', args: 'refresh' })
  json = status({}, { behind: 0, ...ownRemote('rebased') })
  await $.command.run({ ...RUN, command: 'wt', args: 'up' })

  // The session now stands in another worktree of the repository.
  const other = `${PATH}-other`
  json = status({ stack: [member('other', other)] }, { work: 'other', branch: 'feat_wt/other', path: other })
  await $.command.run({ ...RUN, command: 'wt', args: 'refresh' })

  const band = await $.ui.mount({ ...BAND, surface: 'terminal' })
  expect(await partsOf(band)).toEqual(['suggestion:other ↓4↑2 main'])
  await band.unmount()

  const pane = await $.ui.mount({ ...PANE, surface: 'terminal' })
  expect(await pane.find({ type: 'Text', text: /login rebased/ })).toBeUndefined()
  expect(await pane.find({ type: 'Button', key: 'push' })).toBeUndefined()
  expect(await pane.find({ type: 'Button', key: 'undo' })).toBeUndefined()
  await pane.unmount()

  expect((await $.command.run({ ...RUN, command: 'wt', args: 'push' })).text).toBe('Nothing to push from here: run wt up first.')
  expect(shown.fills).toEqual([])
  expect(runs.some(argv => argv.includes('push'))).toBe(false)
})

test('resume acts on the worktree the session stands in, and never pushes', async ($, on) => {
  const { runs, cwds } = world(on, () => status({ upEligible: false, upIneligibleCode: 'handedOver', token: null }, { state: 'dirty' }), {
    'wt sync resume': result('sync resume', 'done', null, [participant({ pushCommand: ['git', '-C', PATH, 'push', 'origin', 'feat_wt/login'] })]),
  })

  await $.command.run({ ...RUN, command: 'wt', args: 'refresh' })
  const pane = await $.ui.mount({ ...PANE, surface: 'terminal' })
  expect(await pane.find({ type: 'Button', key: 'up' })).toBeUndefined()
  await pane.press({ key: 'resume' })
  await pane.unmount()

  const resume = runs.findIndex(argv => argv[2] === 'resume')
  expect(runs[resume]).toEqual(['wt', 'sync', 'resume', '.', '--yes', '--no-push', '--json'])
  expect(cwds[resume]).toBe(PATH)
  expect(runs.some(argv => argv[0] === 'git' && argv.includes('push'))).toBe(false)
})
