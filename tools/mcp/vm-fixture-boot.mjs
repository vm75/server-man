// VM fixture boot precondition for the explicitly acknowledged disposable runner.
// No guest text is returned or logged; only a serial login prompt establishes readiness.
import assert from 'node:assert/strict'
import {spawn} from 'node:child_process'
import {setTimeout as delay} from 'node:timers/promises'

const libvirtFixture = 'server-man-test-libvirt'
const incusFixture = 'server-man-test-incus'

export function fixtureConsole(service, model) {
  if (service === libvirtFixture) {
    return {command:['virsh','--connect','qemu:///system','console','sm-server-man-test-libvirt','--safe'],detach:'\x1d'}
  }
  if (service !== incusFixture) return null
  const binding = model?.config?.services?.[service]?.vm?.project
  const project = model?.config?.bindings?.[binding]?.name
  assert(typeof project === 'string' && /^[A-Za-z0-9][A-Za-z0-9_.-]{0,62}$/.test(project),
    'Prepare a safe local Incus project fixture name')
  return {command:['incus','--force-local','--project',project,'console','local:sm-server-man-test-incus','--type=console'],detach:'\x01q'}
}

function checkSerialLogin(spawnSSH, connectionArgs, {command,detach}) {
  return new Promise(resolve => {
    const child = spawnSSH('ssh',['-tt',...connectionArgs,'timeout','--kill-after=2s','8s',...command],{stdio:['pipe','pipe','pipe']})
    let output = '', found = false, finished = false
    child.stdin.on('error',()=>{})
    child.stderr.resume()
    child.stdout.on('data',data=>{
      output=(output+data.toString()).slice(-4096)
      if (!found && /(?:^|[\r\n])[^\r\n]{0,128} login: /.test(output)) {
        found=true
        child.stdin.write(detach)
      }
    })
    const newline=setTimeout(()=>child.stdin.write('\r'),2000)
    const timeout=setTimeout(()=>child.kill('SIGKILL'),12000)
    const finish=()=>{
      if (finished) return
      finished=true
      clearTimeout(newline)
      clearTimeout(timeout)
      resolve(found)
    }
    child.once('error',finish)
    child.once('close',finish)
  })
}

export async function waitVMFixtureBoot(service,{call,connectionArgs,spawnSSH=spawn,sleep=delay,now=Date.now}) {
  if (service !== libvirtFixture && service !== incusFixture) return
  const model = service === incusFixture ? await call('managed_config_model') : undefined
  const console = fixtureConsole(service,model)
  const deadline=now()+3*60*1000
  while (now()<deadline) {
    if (await checkSerialLogin(spawnSSH,connectionArgs,console)) return
    await sleep(1000)
  }
  throw new Error(service+': prepared VM fixture did not reach serial login; inspect guest boot before further lifecycle operations')
}
