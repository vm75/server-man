import test from 'node:test'
import assert from 'node:assert/strict'
import {EventEmitter} from 'node:events'
import {PassThrough} from 'node:stream'
import {fixtureConsole,waitVMFixtureBoot} from './vm-fixture-boot.mjs'

const libvirt = 'server-man-test-libvirt'
const incus = 'server-man-test-incus'
const model = project => ({config:{services:{[incus]:{vm:{project:'server-man-test-incus-project'}}},bindings:{'server-man-test-incus-project':{name:project}}}})

function fakeSpawn({output='guest login: ',detach,argumentsSeen}) {
  return (command,args,options) => {
    assert.equal(command,'ssh')
    assert.deepEqual(options,{stdio:['pipe','pipe','pipe']})
    argumentsSeen.push(args)
    const child=new EventEmitter()
    child.stdin=new PassThrough()
    child.stdout=new PassThrough()
    child.stderr=new PassThrough()
    child.kill=()=>child.emit('close',null)
    child.stdin.on('data',data=>{
      if (data.toString() === detach) queueMicrotask(()=>child.emit('close',0))
    })
    queueMicrotask(()=>{
      if (output) {
        child.stdout.write(output.slice(0,output.length-2))
        child.stdout.write(output.slice(-2))
        if (!output.includes(' login: ')) queueMicrotask(()=>child.emit('close',0))
      } else child.emit('close',0)
    })
    return child
  }
}

test('libvirt uses the fixed system connection and waits for serial login before returning',async()=>{
  const seen=[]
  await waitVMFixtureBoot(libvirt,{call:()=>{throw Error('libvirt should not read configuration')},connectionArgs:['pinned-ssh'],spawnSSH:fakeSpawn({detach:'\x1d',argumentsSeen:seen})})
  assert.deepEqual(seen,[['-tt','pinned-ssh','timeout','--kill-after=2s','8s','virsh','--connect','qemu:///system','console','sm-server-man-test-libvirt','--safe']])
})

test('Incus uses only the configured local project and its console detach escape',async()=>{
  const seen=[]
  await waitVMFixtureBoot(incus,{call:async name=>{assert.equal(name,'managed_config_model');return model('fixture-project')},connectionArgs:['pinned-ssh'],spawnSSH:fakeSpawn({detach:'\x01q',argumentsSeen:seen})})
  assert.deepEqual(seen,[['-tt','pinned-ssh','timeout','--kill-after=2s','8s','incus','--force-local','--project','fixture-project','console','local:sm-server-man-test-incus','--type=console']])
})

test('unsafe or absent Incus project is refused before invoking SSH',async()=>{
  for (const value of ['','-bad','other;command','project/name',undefined]) {
    const seen=[]
    await assert.rejects(waitVMFixtureBoot(incus,{call:async()=>model(value),connectionArgs:[],spawnSSH:fakeSpawn({detach:'\x01q',argumentsSeen:seen})}),/safe local Incus project/)
    assert.equal(seen.length,0)
  }
})

test('no serial login fails boundedly without echoing guest output',async()=>{
  let current=0
  const seen=[]
  const secret='sensitive-console-output'
  await assert.rejects(waitVMFixtureBoot(libvirt,{call:async()=>{},connectionArgs:[],spawnSSH:fakeSpawn({output:secret,detach:'\x1d',argumentsSeen:seen}),now:()=>{current+=90000;return current},sleep:async()=>{}}),err=>{
    assert.match(err.message,/did not reach serial login/)
    assert.doesNotMatch(err.message,new RegExp(secret))
    return true
  })
  assert.equal(seen.length,1)
})

test('non-VM services do not start a console probe',async()=>{
  await waitVMFixtureBoot('server-man-test-docker-compose',{call:()=>{throw Error('unexpected call')},connectionArgs:[],spawnSSH:()=>{throw Error('unexpected SSH')}})
  assert.equal(fixtureConsole('server-man-test-nfs'),null)
})
