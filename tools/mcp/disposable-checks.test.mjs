import test from 'node:test'
import assert from 'node:assert/strict'
import {verifyGuest,selectedTargets,targets} from './disposable-checks.mjs'

// An in-memory transport contract, not a native backend or guest substitute.
function fixture(options = {}) {
  let revision = 1
  const config = {version:1,server:{allowWrites:true,privilegedOperations:true},services:{},shares:{},secrets:{},storage:{'server-man-test-data':{path:'/srv/server-man-test',kind:'directory',external:true},'server-man-test-libvirt-disk':{path:'/srv/server-man-test/disk.qcow2',kind:'file',format:'qcow2',external:true}},bindings:{}}
  for (const [name,kind] of [['libvirt-network','network'],['incus-network','network'],['incus-pool','pool'],['incus-project','project']]) config.bindings[`server-man-test-${name}`]={kind,name:`fixture-${name}`}
  const states = new Map()
  const calls = [],jobs = []
  const unmanaged = {name:'unmanaged-fixture',path:'/srv/unmanaged-fixture',readOnly:true,managed:false,protocol:'nfs',configured:true,runtime:'unknown'}
  const snapshot = () => ({revision:String(revision).padStart(64,'a'),config:structuredClone(config)})
  const call = async (name,args = {}) => {
    calls.push({name,args})
    if (name === 'system_status') return {hostname:options.hostname ?? 'server-man-test',allowWrites:true}
    if (name === 'managed_config_model') return snapshot()
    if (name === 'managed_config_check') return {checkedAt:new Date().toISOString(),issues:[]}
    if (name === 'managed_resource_plan') {assert.equal(args.revision,snapshot().revision);if (args.id===options.planFailure) throw new Error('Fixture native definition validation failed');return {restartRequired:false,changes:[],files:{issues:[]}}}
    if (name === 'managed_services') return {configured:true,valid:true,services:[...states].map(([id,state])=>({id,enabled:config.services[id].enabled,...state,issues:[]}))}
    if (name === 'managed_shares') return {configured:true,shares:Object.entries(config.shares).map(([id,definition])=>({id,definition,configuration:definition.enabled?'published':'unpublished',issues:[]}))}
    if (name === 'shares_list') return {backends:[{protocol:args.protocol,available:true,canManage:true,setupRequired:false}],shares:args.protocol==='nfs'?[structuredClone(unmanaged)]:[]}
    throw new Error(`Unexpected read operation: ${name}`)
  }
  const job = async (name,args) => {
    jobs.push({name,args}); assert.equal(args.confirm === true || name === 'managed_service_start',true)
    if (name === 'managed_resource_save') {
      assert.equal(args.revision,snapshot().revision,'Stale fixture write')
      const collection = config[args.kind === 'service'?'services':'shares']
      if (args.operation === 'remove') {delete collection[args.id];states.delete(args.id)}
      else {
        const value = JSON.parse(args.definition)
        if (args.kind === 'service') {
          if (value.vm && !value.vm.uuid) {value.vm.uuid='fixture-uuid';value.vm.mac='fixture-mac'}
          const previous = states.get(args.id)
          states.set(args.id,{state:previous?.state ?? 'stopped',configuration:args.mode==='next_start' && previous?.state==='running'?'restart_required':'registered'})
        }
        collection[args.id]=value
      }
      if (options.dropServer) config.server={allowWrites:false}
    } else if (name === 'managed_share_publish') config.shares[args.share].enabled=args.published
    else {
      const action = name.replace('managed_service_','')
      const state = states.get(args.service)
      assert(state,'Action before definition')
      if (action==='enable' || action==='disable') {
        config.services[args.service].enabled=action==='enable'
        if (options.enableStarts && action==='enable') state.state='running'
        if (options.disableStops && action==='disable') state.state='stopped'
      } else {assert(['start','stop','restart'].includes(action));state.state=action==='stop'?'stopped':'running';state.configuration='registered'}
    }
    revision++
  }
  return {call,job,calls,jobs,config}
}
const run = (guest,options = {}) => verifyGuest({...guest,acknowledged:true,image:'fixture/image:preloaded',...options})

test('selection includes all seven targets and rejects ambiguous input',()=>{
  assert.deepEqual(selectedTargets(),targets)
  for (const value of ['', 'docker', 'docker-compose,docker-compose','incus,','libvirt;id']) assert.throws(()=>selectedTargets(value))
})
test('acknowledgement and dedicated identity precede every mutation',async()=>{
  const guest=fixture()
  await assert.rejects(verifyGuest({...guest,acknowledged:false}))
  assert.equal(guest.calls.length,0);assert.equal(guest.jobs.length,0)
  const wrong=fixture({hostname:'actual-workstation'})
  await assert.rejects(run(wrong));assert.equal(wrong.jobs.length,0)
  assert.deepEqual(wrong.calls.map(call=>call.name),['system_status'])
})
test('existing resources, unrelated references and unsafe storage fail before writes',async()=>{
  for (const change of [
    g=>{g.config.services.unrelated={}},g=>{g.config.shares.unrelated={}},
    g=>{g.config.storage.unrelated={}},g=>{g.config.storage['server-man-test-data'].path='/srv/other'},
    g=>{g.config.storage['server-man-test-data'].path='/srv/server-man-test/../other'},
    g=>{g.config.server.privilegedOperations=false},
  ]) {
    const guest=fixture();change(guest)
    await assert.rejects(run(guest));assert.equal(guest.jobs.length,0)
  }
})
test('all fixtures are checked before earlier selected cases may mutate',async()=>{
  const guest=fixture();delete guest.config.bindings['server-man-test-incus-pool']
  await assert.rejects(run(guest,{selected:['docker-compose','incus']}))
  assert.equal(guest.jobs.length,0)
})
test('native definition review for every selected target precedes writes',async()=>{
  const guest=fixture({planFailure:'server-man-test-incus'})
  await assert.rejects(run(guest,{selected:['docker-compose','incus']}))
  assert.equal(guest.jobs.length,0)
})
test('the complete contract uses central writes and retains only stopped Incus',async()=>{
  const guest=fixture();await run(guest)
  assert.deepEqual(Object.keys(guest.config.services),['server-man-test-incus'])
  assert.equal(guest.config.services['server-man-test-incus'].enabled,false)
  assert.deepEqual(guest.config.shares,{})
  assert(guest.jobs.some(job=>job.name==='managed_share_publish'))
  for (const {name,args} of guest.jobs) {
    assert(['managed_resource_save','managed_share_publish','managed_service_start','managed_service_stop','managed_service_restart','managed_service_enable','managed_service_disable'].includes(name),`Unexpected native or package mutation: ${name}`)
    if (name==='managed_resource_save') assert(args.id.startsWith('server-man-test-'))
  }
})
test('current-run and configuration-preservation violations fail acceptance',async()=>{
  for (const options of [{enableStarts:true},{disableStops:true},{dropServer:true}]) {
    const guest=fixture(options)
    await assert.rejects(run(guest,{selected:['docker-compose']}))
    assert(guest.jobs.length>0)
  }
})
