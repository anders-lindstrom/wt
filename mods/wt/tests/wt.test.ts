import { expect, mock, test } from 'claude-code/testing'
import type { On } from 'claude-code'

const PATH = '/repos/app_wt/feat_wt/login'
const TOKEN = '1:0123abcd'

// `wt status --json` for a worktree 4 behind trunk, as wt 8365774 prints it.
const status = (over: Record<string, unknown> = {}, worktree: Record<string, unknown> = {}): string =>
  JSON.stringify({
    schema: 1,
    schemaVersion: '1.6.0',
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
    stack: [{ work: 'login', branch: 'feat_wt/login', path: PATH }],
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
      pushCommand: ['git', '-C', PATH, 'push', '--force-with-lease', '--force-if-includes', 'origin', 'feat_wt/login'],
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
const world = (on: On, statusJson: () => string, answers: Record<string, string> = {}, refs: () => string = () => 'refs') => {
  const runs: string[][] = []
  const cwds: (string | undefined)[] = []
  const shown: { status: string | undefined; toasts: string[]; fills: string[] } = { status: undefined, toasts: [], fills: [] }
  const clock = mock.clock(on, { now: START })

  on('process.run', ($, e) => {
    runs.push([...e.argv])
    cwds.push(e.init?.cwd)
    const key = e.argv.slice(0, 3).join(' ')
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
    shown.fills.push(e.text)

    return { isFilled: true }
  })
  on('prompt.submit', ($, e) => ({ text: e.text }))
  on('skill.prompt', ($, e) => ({ text: e.text }))
  on('turn.start', ($, e) => ({ turnId: e.turnId }))
  on('session.start', ($, e) => ({ cwd: e.cwd }))
  on('command.register', ($, e) => ({ value: { command: e.name } }))

  return { runs, cwds, shown, clock }
}

test('a worktree behind trunk gets a band, and wt up runs with the plan token', async ($, on) => {
  const { runs, cwds, shown } = world(on, () => status())

  expect((await $.command.run({ ...RUN, command: 'wt', args: 'refresh' })).text).toContain('4 behind main')
  expect(shown.status).toBe('login · 4 behind main')

  for (const surface of ['terminal', 'desktop'] as const) {
    const ui = await $.ui.mount({ ...BAND, surface })
    expect(await ui.find({ type: 'Text', text: /login is 4 behind origin\/main/ })).toBeDefined()
    expect(await ui.find({ type: 'Button', key: 'up' })).toBeDefined()
    await ui.unmount()
  }

  const ui = await $.ui.mount({ ...BAND, surface: 'terminal' })
  await ui.press({ key: 'up' })

  expect(runs).toContainEqual(['wt', 'up', '--yes', '--no-push', '--json', '--expect', TOKEN])
  expect(await ui.find({ type: 'Text', text: /login rebased onto origin\/main \(1111111 → 2222222\)/ })).toBeDefined()
  expect(await ui.find({ type: 'Button', key: 'push' })).toBeDefined()
  expect(await ui.find({ type: 'Button', key: 'undo' })).toBeDefined()

  // Push runs nothing: the lease-protected push goes in the prompt.
  await ui.press({ key: 'push' })
  expect(shown.fills).toEqual([`! git -C ${PATH} push --force-with-lease --force-if-includes origin feat_wt/login`])
  expect(runs.some(argv => argv[0] === 'git')).toBe(false)

  // Undo acts on the worktree the command runs in.
  await ui.press({ key: 'undo' })
  expect(runs.at(-2)).toEqual(['wt', 'sync', 'undo', '.', '--yes', '--json'])
  expect(cwds.at(-2)).toBe(PATH)
  await ui.unmount()
})

test('while a turn runs the band says so and offers nothing that moves the branch', async ($, on) => {
  const { runs } = world(on, () => status())

  await $.command.run({ ...RUN, command: 'wt', args: 'refresh' })
  const ui = await $.ui.mount({ ...BAND, surface: 'terminal', props: { ...BAND.props, isWorking: true } })

  expect(await ui.find({ type: 'Text', text: /once this turn is done/ })).toBeDefined()
  expect(await ui.find({ type: 'Button', key: 'up' })).toBeUndefined()
  expect((await $.command.run({ ...RUN, command: 'wt', args: 'up' })).text).toContain('a turn is running')
  expect(runs.some(argv => argv[1] === 'up')).toBe(false)
  await ui.unmount()
})

test('a branch that diverged from its own remote is refused in wt’s words, with no wt up', async ($, on) => {
  const reason = 'feat_wt/login has diverged from origin/feat_wt/login: 2 here, 1 there.'
  const { runs } = world(on, () =>
    status(
      { upEligible: false, upIneligibleCode: 'ownRemoteDiverged', upIneligibleReason: reason },
      { ownRemote: { ref: 'origin/feat_wt/login', state: 'diverged', commit: 'c', ahead: 2, behind: 1, fetched: false, blocks: 'diverged' } },
    ),
  )

  expect((await $.command.run({ ...RUN, command: 'wt', args: 'refresh' })).text).toContain('diverged from its remote')
  const band = await $.ui.mount({ ...BAND, surface: 'terminal' })

  expect(await band.find({ type: 'Text', text: /wt up would refuse: feat_wt\/login has diverged/ })).toBeDefined()
  expect(await band.find({ type: 'Button', key: 'up' })).toBeUndefined()
  await band.unmount()

  // The pane alone offers the override, and it passes the flag with the token.
  const pane = await $.ui.mount({ ...PANE, surface: 'terminal' })
  await pane.press({ key: 'upDiverged' })
  expect(runs).toContainEqual(['wt', 'up', '--yes', '--no-push', '--json', '--expect', TOKEN, '--allow-diverged'])
  await pane.unmount()
})

test('the knobs are off unless asked for', async ($, on) => {
  const { runs, shown } = world(on, () => status())

  await $.command.run({ ...RUN, command: 'wt', args: 'refresh' })
  await $.prompt.submit({ text: 'hello there', origin: { kind: 'composer' }, wait: false })
  expect(shown.status).toBe('login · 4 behind main')
  expect((await $.command.run({ ...RUN, command: 'wt', args: 'gittree' })).text).toContain('not one of')

  const pane = await $.ui.mount({ ...PANE, surface: 'terminal' })
  expect(await pane.find({ type: 'Button', key: 'gittree' })).toBeUndefined()
  expect(await pane.find({ type: 'Text', text: /Last input/ })).toBeUndefined()
  expect(runs.some(argv => argv[0] === 'gittree')).toBe(false)
  await pane.unmount()
})

test(
  'with the knobs on: gittree opens the worktree, and the last review and input are kept',
  { options: { gittree: true, reviewStatus: true } },
  async ($, on) => {
    const { runs, shown, clock } = world(on, () => status())

    await $.command.run({ ...RUN, command: 'wt', args: 'refresh' })
    expect(shown.status).toBe('login · 4 behind main · not reviewed yet')

    await $.prompt.submit({ text: '/tc:pr-review   please look at the diff', origin: { kind: 'composer' }, wait: false })
    await $.skill.prompt({ skill: 'tc:pr-review', text: 'Review the PR.' })
    await $.skill.prompt({ skill: 'tc:commit', text: 'Commit.' })
    expect(shown.status).toBe('login · 4 behind main · reviewed just now, no input since')

    await clock.advance(42 * 60_000)
    await $.prompt.submit({ text: 'another session says hi', origin: { kind: 'peer' }, wait: false })
    await $.prompt.submit({ text: 'now fix the two findings from it', origin: { kind: 'composer' }, wait: false })
    await $.prompt.submit({ text: 'and push', origin: { kind: 'bridge' }, wait: false })
    expect(shown.status).toBe('login · 4 behind main · reviewed 42 min ago, 2 inputs since')

    for (const surface of ['terminal', 'desktop'] as const) {
      const pane = await $.ui.mount({ ...PANE, surface })
      expect(await pane.find({ type: 'Text', text: '/tc:pr-review, 42 min ago, 2 inputs since' })).toBeDefined()
      expect(await pane.find({ type: 'Text', text: 'just now: “and push” (3 so far)' })).toBeDefined()
      await pane.press({ key: 'gittree' })
      await pane.unmount()
    }

    expect(runs).toContainEqual(['gittree', PATH])
    expect(shown.toasts.at(-1)).toBe('Opened login in gittree.')
  },
)

test('a turn that started holds the moves where no band is drawn, and a second press does not start a second run', async ($, on) => {
  const { runs } = world(on, () => status())

  await $.command.run({ ...RUN, command: 'wt', args: 'refresh' })
  await $.turn.start({ text: 'go', turnId: 't1' })
  const pane = await $.ui.mount({ ...PANE, surface: 'mobile' })
  await pane.press({ key: 'up' })
  expect(runs.some(argv => argv[1] === 'up')).toBe(false)
  await pane.unmount()

  // The band, drawn idle, says the turn is over: two presses, one run.
  const band = await $.ui.mount({ ...BAND, surface: 'terminal' })
  await Promise.all([band.press({ key: 'up' }), $.command.run({ ...RUN, command: 'wt', args: 'up' })])
  expect(runs.filter(argv => argv[1] === 'up')).toHaveLength(1)
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

  const band = await $.ui.mount({ ...BAND, surface: 'terminal' })
  expect(await band.find({ type: 'Button', key: 'undo' })).toBeUndefined()
  expect(await band.find({ type: 'Button', key: 'push' })).toBeUndefined()
  // Still behind and still eligible: the way on is to try again.
  expect(await band.find({ type: 'Button', key: 'up' })).toBeDefined()
  await band.unmount()

  expect((await $.command.run({ ...RUN, command: 'wt', args: 'undo' })).text).toBe('wt did not touch login: feat_wt/login has moved since the run')
})

test('a stack that did not settle is not a success and offers no push', async ($, on) => {
  world(on, () => status({ stack: [{ work: 'login' }, { work: 'login-ui' }] }), {
    'wt up --yes': result('up', 'partial', null, [
      participant({ pushCommand: ['git', '-C', PATH, 'push', 'origin', 'feat_wt/login'] }),
      participant({ work: 'login-ui', path: `${PATH}-ui`, result: 'handedOver', reason: 'conflict in app.ts' }),
    ]),
  })

  await $.command.run({ ...RUN, command: 'wt', args: 'refresh' })
  const band = await $.ui.mount({ ...BAND, surface: 'terminal' })
  expect(await band.find({ type: 'Text', text: /moves it together with login-ui/ })).toBeDefined()
  await band.press({ key: 'up' })
  expect(await band.find({ type: 'Text', text: /But in its stack login-ui stopped at a conflict: conflict in app.ts/ })).toBeDefined()
  expect(await band.find({ type: 'Button', key: 'push' })).toBeUndefined()
  expect(await band.find({ type: 'Button', key: 'undo' })).toBeDefined()

  // Hide dismisses the outcome; what is still to do comes back.
  await band.press({ key: 'hide' })
  expect(await band.find({ type: 'Text', text: /login is 4 behind origin\/main/ })).toBeDefined()
  await band.unmount()
})

test('the main checkout and a folder wt does not know stay quiet', async ($, on) => {
  let json = status({ token: null, upEligible: false, upIneligibleCode: 'mainCheckout' }, { work: 'app', branch: 'main', isMain: true, behind: 0, ahead: 0 })
  const { shown } = world(on, () => json)

  expect((await $.command.run({ ...RUN, command: 'wt', args: 'refresh' })).text).toBe('main checkout')
  expect((await $.command.run({ ...RUN, command: 'wt', args: 'up' })).text).toContain('wt up would refuse app')

  json = 'wt: not inside a git repository'
  expect((await $.command.run({ ...RUN, command: 'wt', args: 'refresh' })).text).toBe('No wt worktree here.')
  expect(shown.status).toBeUndefined()
})

test('what another terminal did shows within one pulse, and a quiet repository costs no wt status', async ($, on) => {
  let behind = 4
  let tips = 'refs/remotes/origin/main aaa'
  const { runs, shown, clock } = world(on, () => status({}, { behind }), {}, () => tips)
  const statusRuns = (): number => runs.filter(argv => argv[0] === 'wt' && argv[1] === 'status').length

  await $.session.start({ cwd: PATH, surface: 'terminal', isInteractive: true })
  await clock.settle()
  expect(shown.status).toBe('login · 4 behind main')

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
  expect(shown.status).toBe('login · up to date with main')
})
