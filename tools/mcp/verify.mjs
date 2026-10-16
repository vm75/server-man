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
const root = await mkdtemp(path.join(tmpdir(), 'serverman-mcp-'))
const clients = []
const protocolErrors = []
let bridge

async function exercise(client, label) {
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
  const projects = await client.callTool({ name: 'compose_projects', arguments: {} })
  assert.equal(projects.isError, false)
  assert.deepEqual(projects.structuredContent.projects, [])
  assert.equal(projects.structuredContent.engines.length, 2)
  const invalidProject = await client.callTool({ name: 'compose_status', arguments: { engine: 'podman', project: '../escape' } })
  assert.equal(invalidProject.structuredContent.error.code, 'invalid_arguments')
  const deniedCompose = await client.callTool({ name: 'compose_down', arguments: { engine: 'docker', project: 'demo', confirm: true } })
  assert.equal(deniedCompose.structuredContent.error.code, 'writes_disabled')
  assert(names.includes('machines_list') && names.includes('lxd_launch') && names.includes('lxc_stop') && names.includes('kvm_define'))
  assert.equal(tools.find(t => t.name === 'kvm_undefine').annotations.destructiveHint, true)
  const machines = await client.callTool({ name: 'machines_list', arguments: { backend: 'kvm' } })
  assert.equal(machines.isError, false)
  assert.equal(machines.structuredContent.backends.length, 1)
  assert.equal(machines.structuredContent.backends[0].id, 'kvm')
  assert(Array.isArray(machines.structuredContent.machines))
  const invalidMachine = await client.callTool({ name: 'machines_details', arguments: { backend: 'lxd', name: '../escape' } })
  assert.equal(invalidMachine.structuredContent.error.code, 'invalid_arguments')
  const deniedMachine = await client.callTool({ name: 'kvm_undefine', arguments: { name: 'demo', confirm: true } })
  assert.equal(deniedMachine.structuredContent.error.code, 'writes_disabled')
  await client.ping()
  console.log(`${label}: initialize, ${tools.length} tools, live monitoring, schemas, error results and denied writes passed`)
  return tools
}

try {
  const config = path.join(root, 'stdio.json')
  await writeFile(config, JSON.stringify({ vmRoot: path.join(root, 'empty-vms'), stateDir: path.join(root, 'stdio-state'), tokenFile: path.join(root, 'unused-token'), allowWrites: false }), { mode: 0o600 })
  const stdioClient = new Client({ name: 'serverman-verifier', version: '1.0.0' })
  stdioClient.onerror = error => protocolErrors.push(error)
  clients.push(stdioClient)
  const stdio = new StdioClientTransport({ command: path.join(repo, 'bin/serverman'), args: ['-transport', 'stdio', '-config', config], stderr: 'pipe' })
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
      const timer = setTimeout(() => { pending.delete(id); reject(new Error('HTTP helper request timed out')) }, 10_000)
      pending.set(id, { resolve, reject, timer })
      bridge.stdin.write(JSON.stringify(message) + '\n')
    })
    const headers = new Headers()
    for (const [name, values] of Object.entries(response.headers)) for (const value of values) headers.append(name, value)
    const body = Buffer.from(response.body ?? '', 'base64')
    return new Response(body.length ? body : null, { status: response.status, headers })
  }
  const httpClient = new Client({ name: 'serverman-http-verifier', version: '1.0.0' })
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
