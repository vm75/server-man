// Core acceptance scenarios. Transport and job execution are injected so guard
// contracts can be checked without native workload or package changes.
import assert from 'node:assert/strict'

export const targets = ['podman-quadlet','podman-compose','docker-compose','libvirt','incus','samba','nfs']
export function selectedTargets(value = targets.join(',')) {
  const selected = value.split(',')
  assert(selected.length && selected.every(target => targets.includes(target)), 'Unknown disposable-guest target')
  assert.equal(new Set(selected).size,selected.length,'Duplicate disposable-guest target')
  return selected
}
const prefix = 'server-man-test-'
const dataID = `${prefix}data`
const bindingID = name => `${prefix}${name}`
const same = (a,b) => JSON.stringify(a) === JSON.stringify(b)

function definitionFor(target,image) {
  if (target === 'libvirt') return {kind:'vm',backend:'libvirt',enabled:false,vm:{cpus:1,memoryMiB:1024,disks:[bindingID('libvirt-disk')],network:bindingID('libvirt-network')}}
  if (target === 'incus') return {kind:'vm',backend:'incus',enabled:false,vm:{cpus:1,memoryMiB:1024,image:'server-man-test',rootSizeGiB:8,network:bindingID('incus-network'),project:bindingID('incus-project'),pool:bindingID('incus-pool')}}
  if (target === 'samba' || target === 'nfs') return {protocol:target,storage:dataID,enabled:false,readOnly:true,...(target === 'samba' ? {guest:false,browseable:true,users:['manager']} : {clients:['127.0.0.1'],rootSquash:true,security:'sys'})}
  return {kind:'container',engine:target === 'docker-compose' ? 'docker' : 'podman',backend:target === 'podman-quadlet' ? 'quadlet' : 'compose',enabled:false,containers:{idle:{image,command:['sleep','3600'],memoryMiB:64,mounts:[{storage:dataID,target:'/fixture',readOnly:true}]}}}
}

export async function verifyGuest({call,job,selected = targets,image,acknowledged = false,report = () => {}}) {
  assert.equal(acknowledged,true,'Explicit disposable-guest acknowledgement required')
  selectedTargets(selected.join(','))
  const status = await call('system_status')
  assert.equal(status.hostname,'server-man-test','Refusing mutations outside the dedicated disposable guest')
  assert.equal(status.allowWrites,true,'Guest management writes must be explicitly enabled')
  const baseline = await call('managed_config_model')
  assert.equal(baseline.config.version,1,'Guest must use the unified versioned configuration')
  assert(baseline.config.server?.allowWrites,'Guest must explicitly configure server.allow_writes')
  assert.equal(Object.keys(baseline.config.services ?? {}).length,0,'Guest must start without configured services')
  assert.equal(Object.keys(baseline.config.shares ?? {}).length,0,'Guest must start without configured shares')
  assert.equal(Object.keys(baseline.config.secrets ?? {}).length,0,'Private-file fixtures are not part of this check')
  for (const kind of ['storage','bindings']) {
    assert(Object.keys(baseline.config[kind] ?? {}).every(id => id.startsWith(prefix)),`Unrelated ${kind} in guest configuration`)
  }
  const requireStorage = (id,kind) => {
    const storage = baseline.config.storage?.[id]
    assert(storage && storage.external === true,`Prepare existing external fixture storage ${id}`)
    assert.equal(storage.kind || 'directory',kind,`Wrong fixture storage kind: ${id}`)
    assert(storage.path === '/srv/server-man-test' || storage.path.startsWith('/srv/server-man-test/'),'Fixture storage must stay under /srv/server-man-test')
    assert(!storage.path.split('/').includes('..'),'Fixture storage cannot traverse its root')
    return storage
  }
  const requireBinding = (name,kind) => {
    const id = bindingID(name)
    assert.equal(baseline.config.bindings?.[id]?.kind,kind,`Prepare ${kind} fixture binding ${id}`)
    return id
  }
  // Check every selected target's fixture before the first mutation. Missing
  // Incus/libvirt fixtures must not be discovered after earlier cases changed it.
  for (const target of selected) {
    if (target.includes('podman') || target === 'docker-compose') {
      assert(typeof image === 'string' && image.length > 0 && image.length <= 512 && !image.startsWith('-') && !/\s|\0/.test(image),'Select a preloaded idle-container image with SERVER_MAN_VM_IMAGE')
      requireStorage(dataID,'directory')
    } else if (target === 'libvirt') {
      const disk = requireStorage(bindingID('libvirt-disk'),'file')
      assert(['raw','qcow2'].includes(disk.format),'Prepare a standalone bootable raw/qcow2 libvirt disk')
      requireBinding('libvirt-network','network')
    } else if (target === 'incus') {
      requireBinding('incus-project','project'); requireBinding('incus-pool','pool'); requireBinding('incus-network','network')
    } else {
      requireStorage(dataID,'directory')
      assert(baseline.config.server.privilegedOperations,'Share checks require the guest privileged helper')
    }
  }
  const nativeShares = new Map()
  const nativeDefinitions = inventory => inventory.shares.filter(share=>!share.managed).map(({runtime,connections,...definition})=>definition)
  for (const protocol of selected.filter(target=>['samba','nfs'].includes(target))) {
    const inventory = await call('shares_list',{protocol})
    assert(inventory.backends.some(backend=>backend.protocol===protocol && backend.available && backend.canManage && !backend.setupRequired),`Prepare the ${protocol} daemon/helper before checks`)
    assert(!inventory.shares.some(share=>share.managed),'Guest already contains managed native shares')
    nativeShares.set(protocol,nativeDefinitions(inventory))
  }
  const readiness = await call('managed_config_check')
  assert(Array.isArray(readiness.issues),'Guest prerequisite recheck returned no health issues list')
  assert(!readiness.issues.some(issue => issue.severity !== 'warning'),'Resolve fixture file prerequisites before running checks')
  for (const target of selected) {
    const kind = ['samba','nfs'].includes(target) ? 'share' : 'service'
    const plan = await call('managed_resource_plan',{kind,id:`${prefix}${target}`,operation:'save',revision:baseline.revision,definition:JSON.stringify(definitionFor(target,image))})
    assert.equal(plan.restartRequired,false,'Guest startup settings do not match its configuration')
    assert(!plan.files.issues.some(issue=>issue.severity !== 'warning'),`${target}: resolve definition prerequisites before mutations`)
  }
  const assertPreserved = snapshot => {
    assert(same(snapshot.config.server,baseline.config.server),'Server settings changed during workload edits')
    for (const kind of ['storage','bindings','secrets']) assert(same(snapshot.config[kind] ?? {},baseline.config[kind] ?? {}),`Fixture ${kind} changed during workload edits`)
  }
  const edit = async (kind,id,operation,definition,mode = 'now') => {
    const snapshot = await call('managed_config_model'); assertPreserved(snapshot)
    const args = {kind,id,operation,revision:snapshot.revision,...(definition ? {definition:JSON.stringify(definition)} : {})}
    const plan = await call('managed_resource_plan',args)
    assert.equal(plan.restartRequired,false,'A workload check unexpectedly changed server settings')
    await job('managed_resource_save',{...args,mode,confirm:true})
    const current = await call('managed_config_model'); assertPreserved(current)
    return current
  }
  const serviceState = async (id,state,enabled,configuration) => {
    const inventory = await call('managed_services')
    assert(inventory.configured && inventory.valid,'Managed application inventory unavailable')
    const service = inventory.services.find(service => service.id === id)
    assert(service,`Missing application ${id}`)
    assert.equal(service.state,state,`${id}: unexpected running state`)
    assert.equal(service.enabled,enabled,`${id}: unexpected boot policy`)
    if (configuration) assert.equal(service.configuration,configuration,`${id}: unexpected definition state`)
    assert(!service.issues.some(issue => issue.severity !== 'warning'),`${id}: native prerequisites are blocked or unknown`)
    assertPreserved(await call('managed_config_model'))
  }
  const action = async (id,action) => job(`managed_service_${action}`,{service:id,...(action === 'start' ? {} : {confirm:true})})
  for (const target of selected.filter(target => !['samba','nfs'].includes(target))) {
    const id = `${prefix}${target}`
    let service = definitionFor(target,image)
    const created = await edit('service',id,'save',service)
    const identity = created.config.services[id].vm ? {uuid:created.config.services[id].vm.uuid,mac:created.config.services[id].vm.mac} : null
    if (identity) assert(identity.uuid && identity.mac,'VM persistent identities were not generated')
    await serviceState(id,'stopped',false,'registered')
    // Update while stopped; preserve generated VM identities from the fresh model.
    let snapshot = await call('managed_config_model')
    service = structuredClone(snapshot.config.services[id])
    if (service.vm) service.vm.memoryMiB = 1536
    else service.containers.idle.memoryMiB = 96
    await edit('service',id,'save',service)
    await serviceState(id,'stopped',false,'registered')
    await action(id,'enable'); await serviceState(id,'stopped',true,'registered')
    await action(id,'start'); await serviceState(id,'running',true,'registered')
    await action(id,'disable'); await serviceState(id,'running',false,'registered')
    await action(id,'enable'); await serviceState(id,'running',true,'registered')
    snapshot = await call('managed_config_model');service = structuredClone(snapshot.config.services[id])
    if (service.vm) service.vm.memoryMiB = 2048
    else service.containers.idle.memoryMiB = 128
    if (target === 'incus') {
      // v1 explicitly rejects running hardware edits at review time, even
      // when the user selects Save for next start. No YAML/job is changed.
      const before = await call('managed_config_model')
      await assert.rejects(
        call('managed_resource_plan',{kind:'service',id,operation:'save',revision:before.revision,definition:JSON.stringify(service)}),
        /stop.*Incus|Incus.*stop/i
      )
      const after = await call('managed_config_model')
      assert.equal(after.revision,before.revision,'Rejected running Incus edit changed YAML')
      await action(id,'stop'); await serviceState(id,'stopped',true,'registered')
      await edit('service',id,'save',service,'next_start')
      await serviceState(id,'stopped',true,'registered')
    } else {
      await edit('service',id,'save',service,'next_start')
      await serviceState(id,'running',true,'restart_required')
      await action(id,'stop'); await serviceState(id,'stopped',true,'registered')
    }
    await action(id,'start'); await serviceState(id,'running',true,'registered')
    await action(id,'restart'); await serviceState(id,'running',true,'registered')
    await action(id,'stop'); await serviceState(id,'stopped',true,'registered')
    await action(id,'disable'); await serviceState(id,'stopped',false,'registered')
    if (identity) {const current = (await call('managed_config_model')).config.services[id].vm;assert(current.uuid===identity.uuid && current.mac===identity.mac,'VM identity changed during edits/lifecycle')}
    if (target !== 'incus') {
      await edit('service',id,'remove')
      assert(!(await call('managed_config_model')).config.services?.[id],`${id}: removal did not remove the central definition`)
    }
    report(`${target}: define/update stopped, start/stop/restart, independent boot policy and ${target === 'incus' ? 'running hardware refusal plus explicit stopped update' : 'deferred update'} passed`)
  }
  for (const protocol of selected.filter(target => ['samba','nfs'].includes(target))) {
    const id = `${prefix}${protocol}`
    const definition = definitionFor(protocol,image)
    const assertShare = async (enabled,configuration) => {
      const inventory = await call('managed_shares')
      const share = inventory.shares.find(share => share.id === id)
      assert(share,`Missing share ${id}`)
      assert.equal(share.definition.enabled,enabled)
      assert.equal(share.configuration,configuration)
      assert(!share.issues.some(issue => issue.severity !== 'warning'),`${id}: share prerequisites blocked or unknown`)
      assertPreserved(await call('managed_config_model'))
    }
    await edit('share',id,'save',definition); await assertShare(false,'unpublished')
    await job('managed_share_publish',{share:id,published:true,confirm:true}); await assertShare(true,'published')
    await edit('share',id,'save',{...definition,enabled:true,readOnly:false}); await assertShare(true,'published')
    await job('managed_share_publish',{share:id,published:false,confirm:true}); await assertShare(false,'unpublished')
    await edit('share',id,'remove')
    assert(!(await call('managed_config_model')).config.shares?.[id],`${id}: share definition was not removed`)
    const native = await call('shares_list',{protocol})
    assert(!native.shares.some(share=>share.managed),'Managed native share remained after removal')
    assert(same(nativeDefinitions(native),nativeShares.get(protocol)),'Unmanaged share definitions changed')
    report(`${protocol}: central define/update/publish/unpublish/remove and unmanaged inventory preservation passed`)
  }
  const final = await call('managed_config_model');assertPreserved(final)
  assert.equal(Object.keys(final.config.shares ?? {}).length,0,'Shares remain configured')
  const expectedServices = selected.includes('incus') ? [`${prefix}incus`] : []
  assert(same(Object.keys(final.config.services ?? {}).sort(),expectedServices),'Unexpected remaining services')
  const finalHealth = await call('managed_config_check')
  assert(!finalHealth.issues.some(issue => issue.severity !== 'warning'),'Fixture data/backend readiness no longer intact')
  report('Selected checks passed. Incus remains stopped/boot-disabled when selected; discard the disposable guest after review. Reboot, daemon-restart, guest OS and share-client acceptance are separate checks.')
}
