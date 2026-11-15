// Explicit destructive integration check for an already provisioned disposable
// guest only. VM_DIR contains its SSH key/known_hosts; all remote mutations are
// restricted to this guest and the server-man-test resources prepared there.
import assert from 'node:assert/strict'
import { setTimeout as delay } from 'node:timers/promises'
import path from 'node:path'
import { Client } from '@modelcontextprotocol/sdk/client/index.js'
import { StdioClientTransport } from '@modelcontextprotocol/sdk/client/stdio.js'

assert(process.env.SERVER_MAN_VM_DIR, 'Set SERVER_MAN_VM_DIR to the disposable guest directory')
const root = path.resolve(process.env.SERVER_MAN_VM_DIR)
const port = Number(process.env.SERVER_MAN_VM_PORT ?? 22472)
assert(Number.isInteger(port) && port > 1024 && port <= 65535)
const client = new Client({ name: 'server-man-disposable-vm', version: '1' })
const transport = new StdioClientTransport({ command: 'ssh', args: [
  '-o', 'BatchMode=yes', '-o', 'ConnectTimeout=10', '-o', 'StrictHostKeyChecking=yes',
  '-o', `UserKnownHostsFile=${path.join(root, 'known_hosts')}`, '-i', path.join(root, 'key'),
  '-p', String(port), 'manager@127.0.0.1',
  '/usr/local/bin/server-man',
  '-transport', 'stdio', '-config', '/home/manager/.config/server-man/server-man.yaml',
], stderr: 'pipe' })
transport.stderr?.on('data', data => process.stderr.write(data))
async function call(name, args = {}) {
  const reply = await client.callTool({ name, arguments: args })
  assert.equal(reply.isError, false, `${name}: ${JSON.stringify(reply.structuredContent)}`)
  return reply.structuredContent
}
async function job(name, args) {
  const { job } = await call(name, args)
  assert(job?.id, name)
  const deadline = Date.now() + 25 * 60 * 1000
  while (Date.now() < deadline) {
    const state = await call('jobs_get', { id: job.id })
    if (state.state !== 'running') {
      assert.equal(state.state, 'succeeded', `${name}: ${state.error ?? ''}\n${state.output ?? ''}`)
      console.log(`${name} succeeded`)
      return state.result
    }
    await delay(1000)
  }
  throw new Error(`${name} did not finish`)
}
try {
  await client.connect(transport)
  assert.equal((await call('system_status')).hostname, 'server-man-test', 'Refusing mutations outside the dedicated disposable guest')
  for (const scope of ['system', 'user']) {
    const list = await call('services_list', { scope })
    assert.equal(list.scope, scope)
    assert(list.services.some(s => s.name === 'server-man-test.service'), JSON.stringify({scope, count:list.services.length, warnings:list.warnings, names:list.services.filter(s => s.name.includes('server-man'))}))
    for (const action of ['start', 'reload', 'restart', 'stop', 'enable', 'disable']) {
      await job(`services_${action}`, { scope, unit: 'server-man-test.service', ...(['start', 'enable'].includes(action) ? {} : { confirm: true }) })
    }
  }
  await job('packages_install', { packages: 'hello', confirm: true })
  await job('updates_install', { confirm: true })
  await job('dependencies_install', { target: 'podman', confirm: true })
  for (const protocol of ['samba', 'nfs']) {
    await job('shares_setup', { protocol, confirm: true })
    let list = await call('shares_list', { protocol })
    const options = protocol === 'samba' ? { guest: false, browseable: true, users: 'manager' } : { clients: '127.0.0.1', rootSquash: true, security: 'sys' }
    await job(`${protocol}_share_save`, { name: 'server-man-test', path: '/srv/server-man-test', readOnly: true, revision: list.backends[0].revision, confirm: true, ...options })
    list = await call('shares_list', { protocol })
    assert(list.shares.some(s => s.name === 'server-man-test' && s.managed))
    await job(`${protocol}_share_remove`, { name: 'server-man-test', revision: list.backends[0].revision, confirm: true })
  }
  console.log('Real sudo helper, both service scopes, package install/update, module dependencies, and Samba/NFS transactions passed in disposable VM.')
} finally { await client.close() }
