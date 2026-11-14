import assert from 'node:assert/strict'
import { spawn } from 'node:child_process'
import { createInterface } from 'node:readline'
import { mkdir, mkdtemp, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { firefox } from 'playwright'

const repo = fileURLToPath(new URL('../../', import.meta.url))
const root = await mkdtemp(path.join(tmpdir(), 'serverman-ui-'))
const artifacts = path.join(repo, 'tools/ui/artifacts')
await mkdir(artifacts, { recursive: true })
let bridge, browser
let nextID = 1
const pending = new Map()
const failures = []
const sampleTimes = new Set()
const managementCalls = []
const deniedByServer = []
let fixtureMode = false

const project = { engine: 'podman', name: 'demo', directory: '/tmp/vms/config/podman/demo', file: '/tmp/vms/config/podman/demo/compose.yml', envFiles: ['/tmp/vms/data/demo/10-app.env'], disabled: false, state: 'running', containers: 1, running: 1 }
const fixtures = {
  managed_services: () => ({ configured: true, valid: true, health: { checkedAt: new Date().toISOString(), issues: [{severity: 'error', resource: 'service/website', field: 'path', code: 'missing_path', message: 'A required folder is missing.', remedy: 'Restore the data folder before starting.'}] }, services: [{ id: 'website', name: 'Website', kind: 'container', backend: 'podman/quadlet', enabled: true, state: 'unknown', configuration: 'pending', registered: false, issues: [{severity: 'error', resource: 'service/website', field: 'path', code: 'missing_path', message: 'A required folder is missing.', remedy: 'Restore the data folder before starting.'}] }] }),
  compose_projects: () => ({ engines: [{ name: 'podman', root: '/tmp/vms/config/podman', available: true }, { name: 'docker', root: '/tmp/vms/config/docker', available: true }], projects: [project], warnings: [] }),
  compose_status: () => ({ project, containers: [{ id: '1234567890abcdef', name: 'demo-web-1', service: 'web', image: 'example/web:latest', state: 'running', status: 'Up', health: '', ports: '', infra: false }], pods: [], warnings: [] }),
  compose_file_read: () => ({ content: 'services:\n  app:\n    image: alpine:latest\n', revision: 'a'.repeat(64) }),
  compose_env_read: () => ({ content: 'SECRET=fixture-value\n', revision: 'b'.repeat(64) }),
  machines_list: args => ({ backends: [{ id: args.backend, title: args.backend, available: true, context: 'local', actions: args.backend === 'lxd' ? ['launch', 'start', 'stop', 'restart', 'delete'] : [] }], machines: [] }),
  services_list: args => ({ scope: args.scope ?? 'system', available: true, journalAvailable: true, services: [{ name: 'sshd.service', description: 'OpenSSH server', load: 'loaded', active: 'active', sub: 'running', enabled: 'enabled' }], warnings: [] }),
  services_details: () => ({ name: 'sshd.service', description: 'OpenSSH server', load: 'loaded', active: 'active', sub: 'running', enabled: 'enabled', canStart: false, canStop: true, canReload: true, mainPID: 1234 }),
  shares_list: () => ({ backends: [{ protocol: 'samba', available: true, canManage: true, managedFile: '/tmp/serverman/samba.conf', revision: 'a'.repeat(64), setupRequired: false }, { protocol: 'nfs', available: true, canManage: true, managedFile: '/tmp/serverman/exports', revision: 'b'.repeat(64), setupRequired: false }], shares: [], warnings: [] }),
  updates_status: () => ({ manager: { id: 'apt', title: 'APT', available: true, canRefresh: true }, distribution: 'Ubuntu 24.04', packages: [{ name: 'demo-package', architecture: 'amd64', installed: '1.0', candidate: '1.1', kind: 'upgrade' }], valid: true, notice: 'Fixture package candidates.', cacheSource: 'fixture cache', reboot: { required: false, reason: 'No marker' }, readAt: new Date().toISOString() }),
}

function fixtureFor(tool, args) {
  const factory = fixtures[tool]
  if (factory) return factory(args)
  if (tool === 'machines_details') return { backend: args.backend, name: args.name, kind: 'container', state: 'running', manageable: true, ips: [] }
  if (tool === 'compose_logs') return { text: 'fixture log', truncated: false }
  if (tool === 'services_logs') return { text: 'fixture journal', truncated: false }
  if (tool.startsWith('jobs_')) return null
  managementCalls.push({ tool, args })
  return { job: { id: `fixture-${managementCalls.length}`, tool, state: 'running', createdAt: new Date().toISOString(), output: '', truncated: false } }
}

async function exerciseManagement(page) {
  fixtureMode = true
  await page.reload()
  await page.getByRole('heading', { name: 'Overview', exact: true }).waitFor()
  await page.getByText('Management enabled', { exact: true }).waitFor()

  await page.getByRole('navigation').getByRole('button', { name: 'Applications', exact: true }).click()
  await page.getByRole('heading', { name: 'Configuration needs attention', exact: true }).waitFor()
  await page.getByText('Restore the data folder before starting.', { exact: true }).first().waitFor()
  assert.equal(await page.getByRole('button', { name: 'Start', exact: true }).isDisabled(), true)
  assert.equal(await page.getByRole('button', { name: 'Disable boot startup', exact: true }).isDisabled(), false)
  assert.equal(await page.locator('body').evaluate(el => el.scrollWidth <= window.innerWidth), true, 'application diagnostics overflow')

  await page.getByRole('navigation').getByRole('button', { name: 'Containers' }).click()
  await page.getByRole('button', { name: 'demo', exact: true }).click()
  await page.getByRole('button', { name: 'Edit Compose' }).click()
  let editor = page.getByRole('dialog')
  await editor.getByRole('heading', { name: 'Edit demo' }).waitFor()
  await editor.locator('textarea').fill('services:\n  app:\n    image: alpine:latest\n# updated\n')
  await editor.getByRole('button', { name: 'Save file' }).click()
  await page.getByText('File save started. Follow its validation in Activity.', { exact: true }).waitFor()
  await page.locator('.activity-button').click()
  await page.getByRole('navigation').getByRole('button', { name: 'Virtual machines' }).click()

  await page.getByRole('navigation').getByRole('button', { name: 'Containers' }).click()
  await page.getByRole('button', { name: 'demo', exact: true }).click()
  const envFiles = page.locator('.environment-files')
  await envFiles.locator('summary').click()
  await envFiles.getByRole('button', { name: 'Edit' }).click()
  editor = page.getByRole('dialog')
  await editor.getByRole('heading', { name: 'Edit environment file' }).waitFor()
  await editor.locator('textarea').fill('SECRET=changed-fixture-value\n')
  await editor.getByRole('button', { name: 'Save file' }).click()
  await page.getByText('File save started. Follow its validation in Activity.', { exact: true }).waitFor()
  await page.locator('.activity-button').click()
  await page.getByRole('navigation').getByRole('button', { name: 'Virtual machines' }).click()

  await page.getByRole('navigation').getByRole('button', { name: 'Containers' }).click()
  await page.getByRole('button', { name: 'demo', exact: true }).click()
  await page.getByLabel('Folder path').fill('config/cache')
  await page.getByRole('button', { name: 'Create folder' }).click()
  await page.getByText('Data directory creation started for demo.', { exact: true }).waitFor()
  await page.locator('.activity-button').click()
  await page.getByRole('navigation').getByRole('button', { name: 'Virtual machines' }).click()

  await page.getByRole('navigation').getByRole('button', { name: 'Containers' }).click()
  await page.getByRole('button', { name: 'New project' }).click()
  editor = page.getByRole('dialog')
  await editor.getByLabel('Project name').fill('fixture-new')
  await editor.getByRole('button', { name: 'Create project' }).click()
  await page.getByText('Project creation started. Follow its validation in Activity.', { exact: true }).waitFor()
  await page.locator('.activity-button').click()
  await page.getByRole('navigation').getByRole('button', { name: 'Virtual machines' }).click()

  await page.getByRole('navigation').getByRole('button', { name: 'Containers' }).click()
  await page.getByRole('button', { name: 'demo', exact: true }).click()
  await page.getByRole('button', { name: 'Restart', exact: true }).click()
  const composeDialog = page.getByRole('dialog')
  await composeDialog.getByRole('heading', { name: 'Restart: demo' }).waitFor()
  await composeDialog.getByRole('button', { name: 'Restart', exact: true }).click()
  await page.getByText('Restart started for demo. Follow its output in Activity.', { exact: true }).waitFor()
  await page.locator('.activity-button').click()

  await page.getByRole('navigation').getByRole('button', { name: 'Virtual machines' }).click()
  await page.getByRole('button', { name: 'Launch instance', exact: true }).click()
  const launchForm = page.locator('form.machine-form')
  await launchForm.getByLabel('Instance name').fill('fixture-vm')
  await launchForm.getByLabel('Image alias or fingerprint').fill('ubuntu:24.04')
  await launchForm.getByRole('button', { name: 'Launch instance' }).click()
  await page.getByText('Management job started. Follow its output in Activity.', { exact: true }).waitFor()
  await page.locator('.activity-button').click()

  await page.getByRole('navigation').getByRole('button', { name: 'Services' }).click()
  await page.getByLabel('Service manager').selectOption('user')
  await page.getByText('Current account’s user manager', { exact: true }).waitFor()
  await page.getByLabel('Service manager').selectOption('system')
  await page.getByText('System manager', { exact: true }).waitFor()
  await page.getByRole('button', { name: /sshd\.service/ }).click()
  await page.getByRole('button', { name: 'Stop', exact: true }).click()
  const serviceDialog = page.getByRole('dialog')
  await serviceDialog.getByRole('heading', { name: 'Stop: sshd.service' }).waitFor()
  await serviceDialog.getByRole('button', { name: 'Stop', exact: true }).click()
  await page.getByText('Service job started. Follow its output in Activity.', { exact: true }).waitFor()
  await page.locator('.activity-button').click()

  await page.getByRole('navigation').getByRole('button', { name: 'File shares' }).click()
  await exerciseShareCreation(page, 'documents')
  await page.evaluate(({ id, tool }) => window.dispatchEvent(new CustomEvent('serverman:job-finished', { detail: { id, tool, state: 'succeeded' } })), { id: `fixture-${managementCalls.length}`, tool: 'samba_share_save' })
  await page.getByRole('button', { name: 'Set up server…', exact: true }).click()
  await page.getByRole('dialog').getByRole('button', { name: 'Set up server', exact: true }).click()
  await page.locator('.activity-button').click()


  await page.getByRole('navigation').getByRole('button', { name: 'Updates' }).click()
  await page.getByRole('textbox', { name: 'Install repository packages' }).fill('samba nfs-utils')
  await page.getByRole('button', { name: 'Install packages…', exact: true }).click()
  await page.getByRole('dialog').getByRole('button', { name: 'Install packages', exact: true }).click()
  await page.locator('.activity-button').click()
  await page.evaluate(({ id, tool }) => window.dispatchEvent(new CustomEvent('serverman:job-finished', { detail: { id, tool, state: 'succeeded' } })), { id: `fixture-${managementCalls.length}`, tool: 'packages_install' })
  await page.getByLabel('Module dependencies', { exact: true }).selectOption('podman')
  await page.getByRole('button', { name: 'Install dependencies…', exact: true }).click()
  await page.getByRole('dialog').getByRole('button', { name: 'Install dependencies', exact: true }).click()
  await page.locator('.activity-button').click()
  await page.evaluate(({ id, tool }) => window.dispatchEvent(new CustomEvent('serverman:job-finished', { detail: { id, tool, state: 'succeeded' } })), { id: `fixture-${managementCalls.length}`, tool: 'dependencies_install' })
  await page.getByRole('button', { name: 'Install system updates' }).click()
  const updateDialog = page.getByRole('dialog')
  await updateDialog.getByRole('heading', { name: 'Install system package updates' }).waitFor()
  await updateDialog.getByRole('button', { name: 'Install updates' }).click()
  await page.getByText('System update started. Follow its progress in Activity.', { exact: true }).waitFor()
  await page.locator('.activity-button').click()

  const submitted = Object.fromEntries(managementCalls.map(call => [call.tool, call.args]))
  assert.equal(submitted.compose_restart?.project, 'demo')
  assert.equal(submitted.compose_restart?.confirm, true)
  assert.match(submitted.compose_file_save?.content, /# updated/)
  assert.equal(submitted.compose_file_save?.revision, 'a'.repeat(64))
  assert.equal(submitted.compose_file_save?.confirm, true)
  assert.match(submitted.compose_env_save?.content, /changed-fixture-value/)
  assert.equal(submitted.compose_env_save?.filename, '10-app.env')
  assert.equal(submitted.compose_env_save?.revision, 'b'.repeat(64))
  assert.equal(submitted.compose_env_save?.confirm, true)
  assert.equal(submitted.compose_data_mkdir?.path, 'config/cache')
  assert.equal(submitted.compose_project_create?.project, 'fixture-new')
  assert.match(submitted.compose_project_create?.content, /your-image:tag/)
  assert.equal(submitted.lxd_launch?.name, 'fixture-vm')
  assert.equal(submitted.services_stop?.unit, 'sshd.service')
  assert.equal(submitted.services_stop?.confirm, true)
  assert.equal(submitted.samba_share_save?.revision, 'a'.repeat(64))
  assert.equal(submitted.samba_share_save?.users, 'alice')
  assert.equal(submitted.updates_install?.confirm, true)
  assert.equal(submitted.services_stop?.scope, 'system')
  assert.equal(submitted.packages_install?.packages, 'samba nfs-utils')
  assert.equal(submitted.packages_install?.confirm, true)
  assert.equal(submitted.shares_setup?.protocol, 'samba')
  assert.equal(submitted.shares_setup?.confirm, true)
  assert.equal(submitted.dependencies_install?.target, 'podman')
  assert.equal(submitted.dependencies_install?.confirm, true)
  assert.deepEqual(deniedByServer.sort(), ['compose_project_create', 'compose_file_save', 'compose_env_save', 'compose_data_mkdir', 'compose_restart', 'lxd_launch', 'samba_share_save', 'services_stop', 'updates_install', 'packages_install', 'shares_setup', 'dependencies_install'].sort())
  fixtureMode = false
}

async function exerciseShareCreation(page, name) {
  const callCount = managementCalls.length
  const denialCount = deniedByServer.length
  await page.getByRole('button', { name: 'New share' }).click()
  await page.getByLabel('Share name').fill(name)
  await page.getByLabel('Existing directory on server').fill(`/srv/shares/${name}`)
  await page.getByLabel('Allowed users or groups').fill('alice')
  await page.getByRole('button', { name: 'Review and apply' }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByRole('heading', { name: `Create Samba share: ${name}` }).waitFor()
  assert.equal(await page.locator('body').evaluate(el => el.scrollWidth <= window.innerWidth), true, 'share review dialog overflows horizontally')
  if (name === 'mobile-documents') await page.screenshot({ path: path.join(artifacts, 'mobile-share-review.png') })
  await dialog.getByRole('button', { name: 'Save and apply' }).click()
  await page.getByText('Share job started. Follow validation, apply and any rollback in Activity.', { exact: true }).waitFor()
  await page.locator('.activity-button').click()
  const submitted = managementCalls.slice(callCount).find(call => call.tool === 'samba_share_save')
  assert.equal(submitted?.args.name, name)
  assert.equal(submitted?.args.path, `/srv/shares/${name}`)
  assert.equal(submitted?.args.revision, 'a'.repeat(64))
  assert.equal(submitted?.args.users, 'alice')
  assert.equal(submitted?.args.confirm, true)
  assert.equal(deniedByServer.length, denialCount + 1)
}

try {
  bridge = spawn(path.join(repo, 'bin/mcp-http-bridge'), [root], { stdio: ['pipe', 'pipe', 'pipe'] })
  const ready = new Promise((resolve, reject) => {
    const lines = createInterface({ input: bridge.stdout })
    lines.on('line', line => {
      let message
      try { message = JSON.parse(line) } catch (error) { reject(error); return }
      if (message.ready) { resolve(); return }
      const item = pending.get(message.id)
      if (item) { pending.delete(message.id); item.resolve(message) }
    })
    bridge.once('error', reject)
    bridge.once('exit', code => reject(new Error(`production handler bridge exited (${code})`)))
  })
  await ready
  const send = request => new Promise((resolve, reject) => {
    const id = nextID++
    pending.set(id, { resolve, reject })
    bridge.stdin.write(`${JSON.stringify({ id, ...request })}\n`)
  })
  browser = await firefox.launch({ headless: true })
  for (const viewport of [{ name: 'desktop', width: 1440, height: 1000 }, { name: 'mobile', width: 390, height: 844 }, { name: 'small-mobile', width: 320, height: 740 }]) {
    sampleTimes.clear()
    console.log(`Checking ${viewport.name} ${viewport.width}x${viewport.height}`)
    const context = await browser.newContext({ viewport: { width: viewport.width, height: viewport.height } })
    const page = await context.newPage()
    page.setDefaultTimeout(8000)
    page.on('pageerror', error => failures.push(error.message))
    page.on('console', message => { if (message.type() === 'error') failures.push(message.text()) })
    await page.route('http://127.0.0.1:8080/**', async route => {
      const request = route.request()
      const url = new URL(request.url())
      if (fixtureMode && url.pathname.startsWith('/api/tools/') && request.method() === 'POST') {
        const tool = decodeURIComponent(url.pathname.split('/').at(-1))
        const args = JSON.parse(request.postData() ?? '{}')
        if (tool.startsWith('jobs_')) {
          const reply = await send({ method: request.method(), url: request.url(), headers: request.headers(), body: request.postData() ?? '' })
          await route.fulfill({ status: reply.status, headers: { 'content-type': 'application/json' }, body: Buffer.from(reply.body, 'base64') })
          return
        }
        if (['compose_project_create', 'compose_file_save', 'compose_env_save', 'compose_data_mkdir', 'compose_restart', 'lxd_launch', 'services_stop', 'samba_share_save', 'updates_install', 'packages_install', 'shares_setup', 'dependencies_install'].includes(tool)) {
          const reply = await send({ method: request.method(), url: request.url(), headers: request.headers(), body: request.postData() ?? '' })
          const body = JSON.parse(Buffer.from(reply.body, 'base64').toString('utf8'))
          assert.equal(body.error?.code, 'writes_disabled', `${tool} should remain denied by the real server`)
          deniedByServer.push(tool)
        }
        const fixture = fixtureFor(tool, args)
        await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(fixture ?? { items: [] }) })
        return
      }
      let reply = await send({ method: request.method(), url: request.url(), headers: request.headers(), body: request.postData() ?? '' })
      if (fixtureMode && url.pathname === '/api/status' && reply.status === 200) {
        const status = JSON.parse(Buffer.from(reply.body, 'base64').toString('utf8'))
        status.allowWrites = true
        reply = { ...reply, body: Buffer.from(JSON.stringify(status)).toString('base64') }
      }
      if (request.url().includes('/api/tools/monitor_snapshot') && reply.status === 200) {
        const snapshot = JSON.parse(Buffer.from(reply.body, 'base64').toString('utf8'))
        sampleTimes.add(snapshot.at)
      }
      await route.fulfill({ status: reply.status, headers: Object.fromEntries(Object.entries(reply.headers).map(([k, values]) => [k, values.join(', ')])), body: Buffer.from(reply.body, 'base64') })
    })
    await page.goto('http://127.0.0.1:8080/')
    await page.locator('.login-brand').filter({ hasText: 'server-man' }).waitFor()
    assert.match(await page.title(), /server-man/)
    // The verifier uses the actual token generated by the production handler.
    const { readFile } = await import('node:fs/promises')
    const token = (await readFile(path.join(root, 'token'), 'utf8')).trim()
    await page.getByLabel('Access token').fill(token)
    await page.getByRole('button', { name: 'Sign in' }).click()
    await page.getByRole('heading', { name: 'Overview', exact: true }).waitFor()
    console.log(`${viewport.name}: signed in`)
    await page.getByText('Connected', { exact: true }).waitFor()
    assert.equal(await page.locator('body').evaluate(el => el.scrollWidth <= window.innerWidth), true, `${viewport.name} overview overflows horizontally`)
    for (const name of ['Applications', 'Containers', 'Virtual machines', 'Services', 'File shares', 'Updates', 'Agent access']) {
      if (viewport.width < 900) {
        await page.getByRole('button', { name: 'Toggle navigation' }).click()
        await page.getByRole('navigation').getByRole('button', { name }).click()
      } else {
        await page.getByRole('navigation').getByRole('button', { name }).click()
      }
      await page.getByRole('heading', { name, exact: true }).waitFor()
      console.log(`${viewport.name}: ${name}`)
      assert.equal(await page.locator('body').evaluate(el => el.scrollWidth <= window.innerWidth), true, `${viewport.name} ${name} overflows horizontally`)
    }
    if (viewport.width < 900) await page.getByRole('button', { name: 'Toggle navigation' }).click()
    await page.getByRole('navigation').getByRole('button', { name: 'Overview' }).click()
    await page.getByText(/Live ·/).waitFor()
    await page.waitForTimeout(2300)
    assert(sampleTimes.size >= 2, 'monitor snapshot did not advance across browser polls')
    await page.screenshot({ path: path.join(artifacts, `${viewport.name}.png`), fullPage: true })
    if (viewport.name === 'desktop') await exerciseManagement(page)
    else if (viewport.name === 'mobile') {
      fixtureMode = true
      await page.reload()
      await page.getByRole('heading', { name: 'Overview', exact: true }).waitFor()
      await page.getByText('Management enabled', { exact: true }).waitFor()
      await page.getByRole('button', { name: 'Toggle navigation' }).click()
      await page.getByRole('navigation').getByRole('button', { name: 'File shares' }).click()
      await exerciseShareCreation(page, 'mobile-documents')
      fixtureMode = false
    }
    await page.getByRole('button', { name: 'Sign out' }).click()
    await page.getByLabel('Access token').waitFor()
    await context.close()
  }
  assert.deepEqual(failures, [], `browser errors: ${failures.join('; ')}`)
  console.log('Rendered desktop/mobile navigation, live monitoring, management forms and confirmations in both viewports, payloads, and server-denied fixture writes passed.')
} finally {
  if (browser) await browser.close()
  if (bridge && bridge.exitCode === null) { bridge.stdin.end(); bridge.kill('SIGTERM') }
  await rm(root, { recursive: true, force: true })
}
