// Explicit native integration check for an already provisioned disposable guest.
// Nothing runs against this workstation's management endpoint.
import assert from 'node:assert/strict'
import {setTimeout as delay} from 'node:timers/promises'
import {lstat} from 'node:fs/promises'
import path from 'node:path'
import {Client} from '@modelcontextprotocol/sdk/client/index.js'
import {StdioClientTransport} from '@modelcontextprotocol/sdk/client/stdio.js'
import {selectedTargets,verifyGuest} from './disposable-checks.mjs'
import {waitVMFixtureBoot} from './vm-fixture-boot.mjs'

assert.equal(process.env.SERVER_MAN_VM_CONFIRM,'disposable','Set SERVER_MAN_VM_CONFIRM=disposable to acknowledge guest mutations')
assert(process.env.SERVER_MAN_VM_DIR,'Set SERVER_MAN_VM_DIR to the private disposable-guest directory')
assert(path.isAbsolute(process.env.SERVER_MAN_VM_DIR),'Guest credentials directory must be an absolute path')
const root = path.resolve(process.env.SERVER_MAN_VM_DIR)
const directory = await lstat(root)
assert(directory.isDirectory() && (directory.mode & 0o077) === 0,'Guest credentials directory must be private and not a symlink')
for (const name of ['key','known_hosts']) {
  const info = await lstat(path.join(root,name))
  assert(info.isFile() && (info.mode & 0o077) === 0,`${name} must be an owner-only regular file`)
}
const port = Number(process.env.SERVER_MAN_VM_PORT ?? 22472)
assert(Number.isInteger(port) && port > 1024 && port <= 65535,'Invalid guest port')
const selected = selectedTargets(process.env.SERVER_MAN_VM_TARGETS)
const client = new Client({name:'server-man-disposable-vm',version:'1'})
const connectionArgs = [
  '-o','BatchMode=yes','-o','ConnectTimeout=10','-o','StrictHostKeyChecking=yes',
  '-o',`UserKnownHostsFile=${path.join(root,'known_hosts')}`,'-i',path.join(root,'key'),
  '-p',String(port),'manager@127.0.0.1',
]
const transport = new StdioClientTransport({command:'ssh',args:[
  ...connectionArgs,'/usr/local/bin/server-man',
  '-transport','stdio','-config','/home/manager/.config/server-man/server-man.yaml',
],stderr:'pipe'})
// Native error output stays in the disposable guest's job/audit records; do not
// print configuration, resolved Compose output or private file contents here.
transport.stderr?.resume()
async function call(name,args = {}) {
  const reply = await client.callTool({name,arguments:args})
  assert.equal(reply.isError,false,`${name} failed; inspect the disposable guest`)
  return reply.structuredContent
}

async function job(name,args) {
  const {job} = await call(name,args)
  assert(job?.id,`${name}: missing job ID`)
  const deadline = Date.now()+25*60*1000
  while (Date.now() < deadline) {
    const state = await call('jobs_get',{id:job.id})
    if (state.state !== 'running') {
      assert.equal(state.state,'succeeded',`${name}: job ${job.id} did not succeed; inspect the guest`)
      if (name === 'managed_service_start' || name === 'managed_service_restart') await waitVMFixtureBoot(args.service,{call,connectionArgs})
      return state.result
    }
    await delay(1000)
  }
  await call('jobs_cancel',{id:job.id})
  throw new Error(`${name}: cancellation requested at deadline; inspect job ${job.id} before any further guest mutations`)
}
try {
  await client.connect(transport)
  await verifyGuest({call,job,selected,image:process.env.SERVER_MAN_VM_IMAGE,acknowledged:true,report:message=>console.log(message)})
} finally {await client.close()}
