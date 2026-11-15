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
let readinessScenario = 'blocked'

const fixtures = {
  managed_config_history: () => ({entries:[{revision:'c'.repeat(64),modifiedAt:new Date().toISOString(),valid:true}]}),
  managed_config_history_read: args => ({revision:args.target,content:'version: 1\n',valid:true}),
  managed_config_revert_plan: args => ({plan:{baseRevision:args.revision,desiredRevision:args.target,changes:[{resource:'service/website',backend:'podman/quadlet',operation:'update'}],files:{issues:[]}}}),
  managed_config_readiness: () => ({ configured: true, valid: readinessScenario !== 'invalid', ...(readinessScenario === 'invalid' ? {error:'Configuration is invalid or inaccessible; open Configuration to repair it.'} : {health: {checkedAt:new Date().toISOString(), runtimeCheckedAt:new Date().toISOString(), issues: readinessScenario === 'healthy' ? [] : readinessScenario === 'unknown' ? [{severity:'unknown',resource:'service/website',field:'runtime',code:'check_cancelled',message:'Backend checks could not complete.',remedy:'Restore backend access and retry.'}] : fixtures.managed_services().health.issues}}) }),
  managed_config_check: () => ({ checkedAt:new Date().toISOString(), issues:[] }),
  managed_config_model: () => ({ revision: 'a'.repeat(64), config: { version: 1, services: {}, shares: {}, storage: { documents: {path:'data/documents',kind:'directory'} }, bindings: { lan:{kind:'network',name:'default'}, pool:{kind:'pool',name:'default'}, project:{kind:'project',name:'default'} }, secrets: {} } }),
  managed_config_read: () => ({ content: 'version: 1\n', revision: 'a'.repeat(64), valid: true }),
  managed_apply_status: () => ({ stage: 'complete' }),
  managed_shares: () => ({ configured: true, revision: 'a'.repeat(64), shares: [] }),
  managed_resource_plan: args => ({ baseRevision: args.revision, desiredRevision: 'b'.repeat(64), changes: [{resource: `${args.kind}/${args.id}`,backend:'fixture',operation:args.operation}], files: {issues:[]} }),
  managed_services: () => ({ configured: true, valid: true, health: { checkedAt: new Date().toISOString(), issues: [{severity: 'error', resource: 'service/website', field: 'path', code: 'missing_path', message: 'A required folder is missing.', remedy: 'Restore the data folder before starting.'}] }, services: [{ id: 'website', name: 'Website', kind: 'container', backend: 'podman/quadlet', enabled: true, state: 'unknown', configuration: 'pending', registered: false, issues: [{severity: 'error', resource: 'service/website', field: 'path', code: 'missing_path', message: 'A required folder is missing.', remedy: 'Restore the data folder before starting.'}] }] }),
  services_list: args => ({ scope: args.scope ?? 'system', available: true, journalAvailable: true, services: [{ name: 'sshd.service', description: 'OpenSSH server', load: 'loaded', active: 'active', sub: 'running', enabled: 'enabled' }], warnings: [] }),
  services_details: () => ({ name: 'sshd.service', description: 'OpenSSH server', load: 'loaded', active: 'active', sub: 'running', enabled: 'enabled', canStart: false, canStop: true, canReload: true, mainPID: 1234 }),
  shares_list: () => ({ backends: [{ protocol: 'samba', available: true, canManage: true, managedFile: '/tmp/serverman/samba.conf', revision: 'a'.repeat(64), setupRequired: false }, { protocol: 'nfs', available: true, canManage: true, managedFile: '/tmp/serverman/exports', revision: 'b'.repeat(64), setupRequired: false }], shares: [], warnings: [] }),
  updates_status: () => ({ manager: { id: 'apt', title: 'APT', available: true, canRefresh: true }, distribution: 'Ubuntu 24.04', packages: [{ name: 'demo-package', architecture: 'amd64', installed: '1.0', candidate: '1.1', kind: 'upgrade' }], valid: true, notice: 'Fixture package candidates.', cacheSource: 'fixture cache', reboot: { required: false, reason: 'No marker' }, readAt: new Date().toISOString() }),
}

function fixtureFor(tool, args) {
  const factory = fixtures[tool]
  if (factory) return factory(args)
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

  const globalHealth = page.getByRole('region', {name:'Configuration readiness',exact:true})
  await globalHealth.getByText('Workload configuration alert',{exact:true}).waitFor()
  await globalHealth.getByText('View all configuration issues (1)',{exact:true}).click()
  await globalHealth.getByText('Restore the data folder before starting.',{exact:true}).waitFor()
  await page.getByRole('navigation').getByRole('button', { name: 'Applications', exact: true }).click()
  await globalHealth.getByText('Workload configuration alert',{exact:true}).waitFor()
  await page.getByRole('heading', { name: 'Configuration needs attention', exact: true }).waitFor()
  await page.getByText('Restore the data folder before starting.', { exact: true }).first().waitFor()
  assert.equal(await page.getByRole('button', { name: 'Start', exact: true }).isDisabled(), true)
  assert.equal(await page.getByRole('button', { name: 'Disable boot startup', exact: true }).isDisabled(), false)
  assert.equal(await page.locator('body').evaluate(el => el.scrollWidth <= window.innerWidth), true, 'application diagnostics overflow')
  await page.getByRole('button',{name:'Define application',exact:true}).click()
  const applicationEditor = page.getByRole('region',{name:'Definition editor'})
  await applicationEditor.getByLabel('Resource ID',{exact:true}).fill('fixture-app')
  await applicationEditor.getByLabel('Display name',{exact:true}).fill('Fixture application')
  await applicationEditor.getByLabel('Container engine',{exact:true}).selectOption('docker')
  assert.equal(await applicationEditor.getByLabel('Container backend',{exact:true}).inputValue(),'compose')
  assert.equal(await applicationEditor.getByLabel('Container backend',{exact:true}).isDisabled(),true)
  await applicationEditor.getByLabel('Container image',{exact:true}).fill('docker.io/library/nginx:stable')
  await applicationEditor.getByRole('button',{name:'Review change',exact:true}).click()
  await applicationEditor.getByRole('heading',{name:'Review changes',exact:true}).waitFor()
  await applicationEditor.getByRole('button',{name:'Confirm and save',exact:true}).click()
  await page.getByText('Definition save started. Follow its validation and apply progress in Activity.',{exact:true}).waitFor()
  await page.locator('.activity-button').click()
  const applicationCall = managementCalls.find(call => call.tool === 'managed_resource_save' && call.args.kind === 'service')
  assert.equal(applicationCall.args.id,'fixture-app')
  const applicationDefinition = JSON.parse(applicationCall.args.definition)
  assert.equal(applicationDefinition.engine,'docker')
  assert.equal(applicationDefinition.backend,'compose')
  assert.equal(applicationDefinition.enabled,false)
  assert.equal(applicationDefinition.containers.app.image,'docker.io/library/nginx:stable')


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
  await page.evaluate(({ id, tool }) => window.dispatchEvent(new CustomEvent('serverman:job-finished', { detail: { id, tool, state: 'succeeded' } })), { id: `fixture-${managementCalls.length}`, tool: 'managed_resource_save' })
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

  await page.getByRole('navigation').getByRole('button',{name:'Configuration',exact:true}).click()
  const history = page.getByRole('region',{name:'Configuration history',exact:true})
  await history.getByRole('button',{name:'Inspect cccccccccccc',exact:true}).click()
  assert.equal(await history.getByLabel('Historical configuration',{exact:true}).inputValue(),'version: 1\n')
  await history.getByRole('button',{name:'Review revert',exact:true}).click()
  await history.getByRole('heading',{name:'Review configuration revert',exact:true}).waitFor()
  assert.equal(await history.getByLabel('Revert apply mode',{exact:true}).inputValue(),'next_start')
  await history.getByRole('button',{name:'Confirm configuration revert',exact:true}).click()
  await page.getByText('Configuration revert started. Follow Activity for apply results.',{exact:true}).waitFor()
  await page.locator('.activity-button').click()
  const reverted = managementCalls.find(call => call.tool === 'managed_config_revert')
  assert.deepEqual(reverted.args,{target:'c'.repeat(64),revision:'a'.repeat(64),mode:'next_start',confirm:true})

  const submitted = Object.fromEntries(managementCalls.map(call => [call.tool, call.args]))
  assert.equal(submitted.services_stop?.unit, 'sshd.service')
  assert.equal(submitted.services_stop?.confirm, true)
  assert.equal(submitted.managed_resource_save?.revision, 'a'.repeat(64))
  assert.deepEqual(JSON.parse(submitted.managed_resource_save.definition).users, ['alice'])
  assert.equal(submitted.updates_install?.confirm, true)
  assert.equal(submitted.services_stop?.scope, 'system')
  assert.equal(submitted.packages_install?.packages, 'samba nfs-utils')
  assert.equal(submitted.packages_install?.confirm, true)
  assert.equal(submitted.shares_setup?.protocol, 'samba')
  assert.equal(submitted.shares_setup?.confirm, true)
  assert.equal(submitted.dependencies_install?.target, 'podman')
  assert.equal(submitted.dependencies_install?.confirm, true)
  assert.deepEqual(deniedByServer.sort(), ['managed_resource_save', 'managed_resource_save', 'managed_config_revert', 'services_stop', 'updates_install', 'packages_install', 'shares_setup', 'dependencies_install'].sort())
  // The alert stays mounted on every page; rechecks clear only resolved issues.
  const beforeRepairCalls = managementCalls.length
  readinessScenario = 'unknown'
  await globalHealth.getByRole('button',{name:'Recheck requirements',exact:true}).click()
  await globalHealth.getByText('Configuration readiness is uncertain',{exact:true}).waitFor()
  readinessScenario = 'invalid'
  await globalHealth.getByRole('button',{name:'Recheck requirements',exact:true}).click()
  await globalHealth.getByText('Workload configuration needs repair',{exact:true}).waitFor()
  readinessScenario = 'healthy'
  await globalHealth.getByRole('button',{name:'Recheck requirements',exact:true}).click()
  await globalHealth.waitFor({state:'detached'})
  assert.equal(managementCalls.length,beforeRepairCalls,'readiness checks must not start or modify workloads')
  readinessScenario = 'blocked'
  await page.reload()
  await page.getByRole('region',{name:'Configuration readiness',exact:true}).getByText('Workload configuration alert',{exact:true}).waitFor()

  fixtureMode = false
}

async function exerciseShareCreation(page, name) {
  const callCount = managementCalls.length
  const denialCount = deniedByServer.length
  await page.getByRole('button', {name:'Define file share',exact:true}).click()
  const editor = page.getByRole('region', {name:'Definition editor'})
  await editor.getByLabel('Resource ID',{exact:true}).fill(name)
  await editor.getByLabel('Shared storage',{exact:true}).selectOption('documents')
  await editor.getByLabel('Local users and @groups',{exact:true}).fill('alice')
  await editor.getByLabel('Publish this share',{exact:true}).check()
  await editor.getByRole('button',{name:'Review change',exact:true}).click()
  await editor.getByRole('heading',{name:'Review changes',exact:true}).waitFor()
  assert.equal(await page.locator('body').evaluate(el => el.scrollWidth <= window.innerWidth),true,'central share editor overflows')
  if (name === 'mobile-documents') await page.screenshot({path:path.join(artifacts,'mobile-share-review.png')})
  await editor.getByRole('button',{name:'Confirm and save',exact:true}).click()
  await page.getByText('Share definition save started. Follow Activity for validation and publication.',{exact:true}).waitFor()
  await page.locator('.activity-button').click()
  const submitted = managementCalls.slice(callCount).find(call => call.tool === 'managed_resource_save')
  assert.equal(submitted?.args.kind,'share')
  assert.equal(submitted?.args.id,name)
  assert.equal(submitted?.args.revision,'a'.repeat(64))
  assert.equal(submitted?.args.confirm,true)
  const definition = JSON.parse(submitted.args.definition)
  assert.deepEqual(definition.users,['alice'])
  assert.equal(definition.storage,'documents')
  assert.equal(definition.enabled,true)
  assert.equal(deniedByServer.length,denialCount+1)
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
        if (['services_stop', 'managed_resource_save', 'managed_config_revert', 'updates_install', 'packages_install', 'shares_setup', 'dependencies_install'].includes(tool)) {
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
    for (const name of ['Applications', 'Configuration', 'Services', 'File shares', 'Updates', 'Agent access']) {
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
      await page.getByRole('region',{name:'Configuration readiness',exact:true}).getByText('Workload configuration alert',{exact:true}).waitFor()
      assert.equal(await page.locator('body').evaluate(el => el.scrollWidth <= window.innerWidth),true,'mobile configuration alert overflows')
      fixtureMode = false
    } else {
      fixtureMode = true
      await page.reload()
      await page.getByRole('region',{name:'Configuration readiness',exact:true}).getByText('Workload configuration alert',{exact:true}).waitFor()
      assert.equal(await page.locator('body').evaluate(el => el.scrollWidth <= window.innerWidth),true,'small-mobile configuration alert overflows')
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
