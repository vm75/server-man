// Explicit native integration check for an already provisioned disposable guest.
// Nothing runs against this workstation's management endpoint.
import assert from 'node:assert/strict'
import {setTimeout as delay} from 'node:timers/promises'
import {lstat} from 'node:fs/promises'
import path from 'node:path'
import {spawn} from 'node:child_process'
import {Client} from '@modelcontextprotocol/sdk/client/index.js'
import {StdioClientTransport} from '@modelcontextprotocol/sdk/client/stdio.js'
import {selectedTargets,verifyGuest} from './disposable-checks.mjs'

assert.equal(process.env.SERVER_MAN_VM_CONFIRM,'disposable','Set SERVER_MAN_VM_CONFIRM=disposable to acknowledge guest mutations')
assert(process.env.SERVER_MAN_VM_DIR,'Set SERVER_MAN_VM_DIR to the private disposable-guest directory')
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

// The prepared BIOS fixture must finish booting before it can receive an ACPI
// shutdown. A running QEMU process alone does not establish that precondition.
// Probe only the dedicated fixture's serial login; never emit console contents.
async function waitLibvirtFixtureBoot(service) {
  if (service !== 'server-man-test-libvirt') return
  const deadline = Date.now()+3*60*1000
  while (Date.now() < deadline) {
    const ready = await new Promise(resolve => {
      const child = spawn('ssh',['-tt',...connectionArgs,'timeout','--kill-after=2s','8s','virsh','--connect','qemu:///system','console','sm-server-man-test-libvirt','--safe'],{stdio:['pipe','pipe','pipe']})
      let output = '', found = false
      child.stdin.on('error',()=>{})
      child.stderr.resume()
      child.stdout.on('data',data=>{
        output=(output+data.toString()).slice(-4096)
        if (!found && /(?:^|[\r\n])[^\r\n]{0,128} login: /.test(output)) {
          found=true
          child.stdin.write('\x1d')
        }
      })
      const newline=setTimeout(()=>child.stdin.write('\r'),2000)
      const timeout=setTimeout(()=>child.kill('SIGKILL'),12000)
      const finish=()=>{clearTimeout(newline);clearTimeout(timeout);resolve(found)}
      child.once('error',finish)
      child.once('close',finish)
    })
    if (ready) return
    await delay(1000)
  }
  throw new Error('Prepared libvirt fixture did not reach serial login; inspect guest boot before further lifecycle operations')
}
async function job(name,args) {
  const {job} = await call(name,args)
  assert(job?.id,`${name}: missing job ID`)
  const deadline = Date.now()+25*60*1000
  while (Date.now() < deadline) {
    const state = await call('jobs_get',{id:job.id})
    if (state.state !== 'running') {
      assert.equal(state.state,'succeeded',`${name}: job ${job.id} did not succeed; inspect the guest`)
      if (name === 'managed_service_start' || name === 'managed_service_restart') await waitLibvirtFixtureBoot(args.service)
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
