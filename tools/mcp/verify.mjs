import assert from 'node:assert/strict'
import { spawn } from 'node:child_process'
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { createInterface } from 'node:readline'
import { fileURLToPath } from 'node:url'
import { Client } from '@modelcontextprotocol/sdk/client/index.js'
import { StdioClientTransport } from '@modelcontextprotocol/sdk/client/stdio.js'
import { StreamableHTTPClientTransport } from '@modelcontextprotocol/sdk/client/streamableHttp.js'

const repo = fileURLToPath(new URL('../../', import.meta.url))
const root = await mkdtemp(path.join(tmpdir(), 'server-man-mcp-'))
const clients = []
const protocolErrors = []
let bridge

async function exercise(client, label) {
  assert.equal(client.getServerVersion().name, 'server-man')
  assert.equal(client.getServerVersion().title, 'server-man')
  const { tools } = await client.listTools()
  const names = tools.map(t => t.name)
  assert(names.includes('monitor_snapshot') && names.includes('system_capabilities') && names.includes('jobs_cancel'))
  assert.equal(tools.find(t => t.name === 'monitor_snapshot').annotations.readOnlyHint, true)
  assert.equal(tools.find(t => t.name === 'jobs_cancel').annotations.readOnlyHint, false)
  const snapshot = await client.callTool({ name: 'monitor_snapshot', arguments: {} })
  assert.equal(snapshot.isError, false)
  assert.equal(snapshot.structuredContent.valid, true)
  assert(snapshot.structuredContent.memory.total > 0 && snapshot.structuredContent.cpu.cores.length > 0)
  assert.deepEqual(JSON.parse(snapshot.content[0].text), snapshot.structuredContent)
  const invalid = await client.callTool({ name: 'monitor_processes', arguments: { limit: 501 } })
  assert.equal(invalid.isError, true)
  assert.equal(invalid.structuredContent.error.code, 'invalid_arguments')
  const denied = await client.callTool({ name: 'jobs_cancel', arguments: { id: 'no-job' } })
  assert.equal(denied.isError, true)
  assert.equal(denied.structuredContent.error.code, 'writes_disabled')
  await assert.rejects(client.callTool({ name: 'unknown_tool', arguments: {} }))
  const jobs = await client.callTool({ name: 'jobs_list', arguments: {} })
  assert.deepEqual(jobs.structuredContent.items, [])
  for (const name of ['managed_config_read', 'managed_config_validate', 'managed_config_health']) {
    assert(names.includes(name))
    assert.equal(tools.find(t => t.name === name).annotations.readOnlyHint, true)
  }
  for (const name of ['managed_config_plan', 'managed_config_save', 'managed_apply_status', 'managed_apply_retry']) assert(names.includes(name))
  const applyStatus = await client.callTool({ name: 'managed_apply_status', arguments: {} })
  assert.equal(applyStatus.structuredContent.stage, 'none')
  const deniedSave = await client.callTool({ name: 'managed_config_save', arguments: { content: 'version: 1\n', revision: 'missing', mode: 'now', confirm: true } })
  assert.equal(deniedSave.structuredContent.error.code, 'writes_disabled')
  const deniedRetry = await client.callTool({ name: 'managed_apply_retry', arguments: { confirm: true } })
  assert.equal(deniedRetry.structuredContent.error.code, 'writes_disabled')
  for (const action of ['start', 'stop', 'restart', 'enable', 'disable']) {
    const name = `managed_service_${action}`
    assert(names.includes(name))
    const deniedService = await client.callTool({ name, arguments: { service: 'website', ...(action === 'start' ? {} : { confirm: true }) } })
    assert.equal(deniedService.structuredContent.error.code, 'writes_disabled')
  }
  assert(names.includes('managed_services') && names.includes('managed_config_check'))
  const managed = await client.callTool({ name: 'managed_services', arguments: {} })
  assert.equal(managed.structuredContent.configured, false)
  assert.deepEqual(managed.structuredContent.services, [])
  assert(names.includes('managed_shares') && names.includes('managed_share_publish'))
  assert(!names.includes('shares_definitions_apply') && !names.includes('shares_definitions_validate'))
  const configuredShares = await client.callTool({ name: 'managed_shares', arguments: {} })
  assert.equal(configuredShares.structuredContent.configured, false)
  assert.deepEqual(configuredShares.structuredContent.shares, [])
  const deniedSharePublish = await client.callTool({ name: 'managed_share_publish', arguments: { share: 'documents', published: false, confirm: true } })
  assert.equal(deniedSharePublish.structuredContent.error.code, 'writes_disabled')
  for (const name of ['managed_config_model','managed_resource_plan','managed_resource_save']) assert(names.includes(name))
  const deniedModel = await client.callTool({name:'managed_config_model',arguments:{}})
  assert.equal(deniedModel.structuredContent.error.code,'writes_disabled')
  const resourceArgs = {kind:'storage',id:'documents',operation:'save',definition:JSON.stringify({path:'data/documents'}),revision:'missing'}
  const plannedResource = await client.callTool({name:'managed_resource_plan',arguments:resourceArgs})
  assert.equal(plannedResource.isError,false)
  const deniedResource = await client.callTool({name:'managed_resource_save',arguments:{...resourceArgs,mode:'now',confirm:true}})
  assert.equal(deniedResource.structuredContent.error.code,'writes_disabled')
  for (const name of ['managed_config_history','managed_config_history_read','managed_config_revert_plan']) {
    const args = name === 'managed_config_history' ? {} : {target:'a'.repeat(64),...(name === 'managed_config_revert_plan' ? {revision:'missing'} : {})}
    const denied = await client.callTool({name,arguments:args})
    assert.equal(denied.structuredContent.error.code,'writes_disabled')
  }
  const deniedRevert = await client.callTool({name:'managed_config_revert',arguments:{target:'a'.repeat(64),revision:'missing',mode:'next_start',confirm:true}})
  assert.equal(deniedRevert.structuredContent.error.code,'writes_disabled')
  const deniedStoragePlan = await client.callTool({name:'managed_storage_plan',arguments:{storage:'documents',revision:'missing',sizeGiB:0}})
  assert.equal(deniedStoragePlan.structuredContent.error.code,'writes_disabled')
  const deniedStorageCreate = await client.callTool({name:'managed_storage_create',arguments:{storage:'documents',revision:'missing',sizeGiB:0,reviewToken:'a'.repeat(64),purpose:'new_data',confirm:true}})
  assert.equal(deniedStorageCreate.structuredContent.error.code,'writes_disabled')
  const deniedBackupRelease = await client.callTool({name:'managed_backup_release',arguments:{revision:'a'.repeat(64),reviewToken:'b'.repeat(64),confirm:true}})
  assert.equal(deniedBackupRelease.structuredContent.error.code,'writes_disabled')
  const deniedBackupPublish = await client.callTool({name:'managed_backup_publish',arguments:{revision:'a'.repeat(64),reviewToken:'b'.repeat(64),confirm:true}})
  assert.equal(deniedBackupPublish.structuredContent.error.code,'writes_disabled')
  const deniedBackupRecoveryStatus = await client.callTool({name:'managed_backup_recovery_status',arguments:{}})
  assert.equal(deniedBackupRecoveryStatus.structuredContent.error.code,'writes_disabled')
  const deniedBackupRecovery = await client.callTool({name:'managed_backup_recover',arguments:{revision:'a'.repeat(64),reviewToken:'b'.repeat(64),confirm:true}})
  assert.equal(deniedBackupRecovery.structuredContent.error.code,'writes_disabled')
  const deniedBackupInventory = await client.callTool({name:'managed_backup_inventory',arguments:{}})
  assert.equal(deniedBackupInventory.structuredContent.error.code,'writes_disabled')
  const deniedBackupPrepare = await client.callTool({name:'managed_backup_prepare',arguments:{revision:'a'.repeat(64),destination:'/var/backups/test',confirm:true}})
  assert.equal(deniedBackupPrepare.structuredContent.error.code,'writes_disabled')
  const deniedConfig = await client.callTool({ name: 'managed_config_read', arguments: {} })
  assert.equal(deniedConfig.structuredContent.error.code, 'writes_disabled')
  const readiness = await client.callTool({name:'managed_config_readiness',arguments:{}})
  assert.equal(readiness.isError,false)
  assert.equal(readiness.structuredContent.configured,false)
  assert.equal(readiness.structuredContent.valid,false)
  const configHealth = await client.callTool({ name: 'managed_config_health', arguments: {} })
  assert.equal(configHealth.isError, false)
  assert.equal(configHealth.structuredContent.configured, false)
  assert.equal(configHealth.structuredContent.runtimeChecked, false)
  const configValidation = await client.callTool({ name: 'managed_config_validate', arguments: { content: 'version: 1\n' } })
  assert.equal(configValidation.isError, false)
  assert.equal(configValidation.structuredContent.valid, true)
  const imageConfig = 'version: 1\nstorage:\n  disk:\n    kind: file\n    path: data/guest.qcow2\n    format: qcow2\n    source:\n      path: assets/base.raw\n      format: raw\n      sha256: '+ 'a'.repeat(64) +'\n'
  const imageValidation = await client.callTool({name:'managed_config_validate',arguments:{content:imageConfig}})
  assert.equal(imageValidation.structuredContent.valid,true)
  const invalidConfig = await client.callTool({ name: 'managed_config_validate', arguments: { content: 'version: 2\n' } })
  assert.equal(invalidConfig.structuredContent.error.code, 'invalid_arguments')
  for (const retired of ['compose_projects','compose_file_save','compose_env_read','compose_project_create','machines_list','lxd_launch','lxc_stop','kvm_define','kvm_undefine','samba_share_save','nfs_share_remove']) assert(!names.includes(retired), `${retired} must not bypass the central configuration`)
  const services = await client.callTool({ name: 'services_list', arguments: {} })
  assert.equal(services.isError, false)
  assert.equal(services.structuredContent.scope, 'system')
  assert(Array.isArray(services.structuredContent.services))
  const invalidService = await client.callTool({ name: 'services_details', arguments: { unit: '*.service' } })
  assert.equal(invalidService.structuredContent.error.code, 'invalid_arguments')
  const deniedService = await client.callTool({ name: 'services_restart', arguments: { unit: 'demo.service', confirm: true } })
  assert.equal(deniedService.structuredContent.error.code, 'writes_disabled')
  const shares = await client.callTool({ name: 'shares_list', arguments: { protocol: 'nfs' } })
  assert.equal(shares.isError, false)
  assert.equal(shares.structuredContent.backends[0].protocol, 'nfs')
  assert(Array.isArray(shares.structuredContent.shares))
  const invalidShare = await client.callTool({ name: 'shares_list', arguments: { protocol: 'ftp' } })
  assert.equal(invalidShare.structuredContent.error.code, 'invalid_arguments')
  const updates = await client.callTool({ name: 'updates_status', arguments: {} })
  assert.equal(updates.isError, false)
  assert.equal(typeof updates.structuredContent.manager.available, 'boolean')
  assert.equal(typeof updates.structuredContent.valid, 'boolean')
  assert(Array.isArray(updates.structuredContent.packages))
  assert.equal(tools.find(t => t.name === 'updates_install').annotations.destructiveHint, true)
  const invalidUpdate = await client.callTool({ name: 'updates_refresh', arguments: { command: 'upgrade' } })
  assert.equal(invalidUpdate.structuredContent.error.code, 'invalid_arguments')
  for (const name of ['updates_refresh', 'updates_install']) {
    const deniedUpdate = await client.callTool({ name, arguments: name === 'updates_install' ? { confirm: true } : {} })
    assert.equal(deniedUpdate.structuredContent.error.code, 'writes_disabled')
  }
  for (const [name, args] of [
    ['packages_install', { packages: 'samba', confirm: true }],
    ['dependencies_install', { target: 'podman', confirm: true }],
    ['shares_setup', { protocol: 'nfs', confirm: true }],
    ['services_restart', { scope: 'user', unit: 'demo.service', confirm: true }],
  ]) {
    assert(names.includes(name))
    const result = await client.callTool({ name, arguments: args })
    assert.equal(result.structuredContent.error.code, 'writes_disabled')
  }
  const badScope = await client.callTool({ name: 'services_list', arguments: { scope: 'other' } })
  assert.equal(badScope.structuredContent.error.code, 'invalid_arguments')
  assert.deepEqual(tools.find(t => t.name === 'services_list').inputSchema.properties.scope.enum, ['system', 'user'])
  await client.ping()
  console.log(`${label}: initialize, ${tools.length} tools, live monitoring, schemas, error results and denied writes passed`)
  return tools
}

try {
  const config = path.join(root, 'stdio.json')
  await writeFile(config, JSON.stringify({ stateDir: path.join(root, 'stdio-state'), tokenFile: path.join(root, 'unused-token'), allowWrites: false }), { mode: 0o600 })
  const stdioClient = new Client({ name: 'server-man-verifier', version: '1.0.0' })
  stdioClient.onerror = error => protocolErrors.push(error)
  clients.push(stdioClient)
  const stdio = new StdioClientTransport({ command: path.join(repo, 'bin/server-man'), args: ['-transport', 'stdio', '-config', config], stderr: 'pipe' })
  await stdioClient.connect(stdio)
  const stdioTools = await exercise(stdioClient, 'Official SDK / stdio')

  bridge = spawn(path.join(repo, 'bin/mcp-http-bridge'), [path.join(root, 'http-state')], { stdio: ['pipe', 'pipe', 'pipe'] })
  const pending = new Map()
  let sequence = 0
  let readyResolve, readyReject
  const ready = new Promise((resolve, reject) => { readyResolve = resolve; readyReject = reject })
  const lines = createInterface({ input: bridge.stdout })
  lines.on('line', line => {
    const message = JSON.parse(line)
    if (message.ready) { readyResolve(); return }
    const entry = pending.get(message.id)
    if (entry) { pending.delete(message.id); clearTimeout(entry.timer); entry.resolve(message) }
  })
  const ended = new Promise(resolve => bridge.once('exit', resolve))
  bridge.once('error', error => { readyReject(error); for (const entry of pending.values()) { clearTimeout(entry.timer); entry.reject(error) }; pending.clear() })
  bridge.once('exit', code => { const error = new Error(`HTTP verification helper exited (${code})`); readyReject(error); for (const entry of pending.values()) { clearTimeout(entry.timer); entry.reject(error) }; pending.clear() })
  await ready
  const token = (await readFile(path.join(root, 'http-state/token'), 'utf8')).trim()
  const fetchInProcess = async (input, init) => {
    const request = new Request(input, init)
    const id = ++sequence
    const message = { id, method: request.method, url: request.url, headers: Object.fromEntries(request.headers), body: await request.text() }
    const response = await new Promise((resolve, reject) => {
      const timer = setTimeout(() => { pending.delete(id); reject(new Error('HTTP helper request timed out')) }, 30_000)
      pending.set(id, { resolve, reject, timer })
      bridge.stdin.write(JSON.stringify(message) + '\n')
    })
    const headers = new Headers()
    for (const [name, values] of Object.entries(response.headers)) for (const value of values) headers.append(name, value)
    const body = Buffer.from(response.body ?? '', 'base64')
    return new Response(body.length ? body : null, { status: response.status, headers })
  }
  const httpClient = new Client({ name: 'server-man-http-verifier', version: '1.0.0' })
  httpClient.onerror = error => protocolErrors.push(error)
  clients.push(httpClient)
  await httpClient.connect(new StreamableHTTPClientTransport(new URL('http://127.0.0.1:8080/mcp'), { fetch: fetchInProcess, requestInit: { headers: { Authorization: `Bearer ${token}` } } }))
  const httpTools = await exercise(httpClient, 'Official SDK / Streamable HTTP through production handler')
  assert.deepEqual(httpTools, stdioTools)
  const discoveryResponse = await fetchInProcess('http://127.0.0.1:8080/api/tools', { headers: { Authorization: `Bearer ${token}` } })
  const discovery = await discoveryResponse.json()
  assert.deepEqual(discovery.tools.map(t => t.name), httpTools.map(t => t.name))
  for (const tool of httpTools) assert.deepEqual(tool.inputSchema, discovery.tools.find(t => t.name === tool.name).inputSchema)
  assert.equal((await fetchInProcess('http://127.0.0.1:8080/mcp')).status, 401)
  assert.equal((await fetchInProcess('http://127.0.0.1:8080/mcp', { headers: { Authorization: `Bearer ${token}`, Origin: 'https://untrusted.example' } })).status, 403)
  console.log('Web API / MCP tool and schema parity, HTTP authentication and origin rejection passed')
  assert.deepEqual(protocolErrors, [], 'Unexpected protocol errors or non-protocol stdout')
  await httpClient.close()
  bridge.stdin.end()
  await ended
} finally {
  for (const client of clients) await client.close().catch(() => {})
  if (bridge && bridge.exitCode === null) bridge.kill()
  await rm(root, { recursive: true, force: true })
}
