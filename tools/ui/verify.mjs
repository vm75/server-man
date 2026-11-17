import assert from 'node:assert/strict'
import { spawn } from 'node:child_process'
import { createInterface } from 'node:readline'
import { mkdir, mkdtemp, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { firefox } from 'playwright'

const repo = fileURLToPath(new URL('../../', import.meta.url))
const root = await mkdtemp(path.join(tmpdir(), 'server-man-ui-'))
const artifacts = path.join(repo, 'tools/ui/artifacts')
await mkdir(artifacts, { recursive: true })
let bridge, browser
let nextID = 1
const pending = new Map()
const failures = []
const sampleTimes = new Set()
const managementCalls = []
const deniedByServer = []
const backupJobResults = new Map()
let fixtureMode = false
let readinessScenario = 'blocked'
let applicationsScenario = 'blocked'
let applyScenario = 'complete'
const artifactConflictMessage = 'Generated files differ from server-man\'s expected output. Configuration remains saved, but publication is blocked. Inspect and preserve this resource\'s owned artifacts before explicit repair and retry.'
let backupRecoveryPending = false
let backupPublicationPending = false
let backupPublicationFinalized = true
let backupRecoveryCurrentRevision = 'a'.repeat(64)

const fixtures = {
  managed_backup_recovery_status: () => backupPublicationPending ? {currentRevision:'a'.repeat(64),journal:{version:1,stage:'complete',revision:'a'.repeat(64),destination:'/tmp/interrupted-capture',updatedAt:new Date().toISOString(),services:{},artifact:{stage:backupPublicationFinalized?'publishing':'building',stagingPath:'/tmp/.staging-backup-fixture',manifestChecksum:backupPublicationFinalized?'e'.repeat(64):undefined}},reviewToken:'c'.repeat(64)} : backupRecoveryPending ? {currentRevision:backupRecoveryCurrentRevision,journal:{version:1,stage:'recovering',revision:'a'.repeat(64),destination:'/tmp/interrupted-capture',updatedAt:new Date().toISOString(),services:{app:'failed',db:'resumed',guest:'stop_uncertain'}},reviewToken:'c'.repeat(64)} : {},
  managed_backup_inventory: () => ({stage:'inventory',revision:'a'.repeat(64),checkedAt:new Date().toISOString(),inputs:[{id:'storage/documents',kind:'directory',strategy:'bundle_copy',path:'/tmp/data/documents',bundlePath:'data/documents',resources:['storage/documents','share/files'],required:true,sensitive:true,inspection:'metadata_only'},{id:'native/guest',kind:'incus_instance_export',strategy:'native_export',native:{backend:'incus',name:'sm-guest',project:'managed',pool:'default'},runtimeState:'stopped',resources:['service/guest'],required:true,sensitive:true,inspection:'native_dependencies_unchecked'}],requirements:[{resource:'storage/documents',kind:'tree_scan',description:'Recursively inventory links and metadata before copy.'}],exclusions:[{kind:'derived_native_state',reason:'Regenerate owned native definitions from the selected configuration.'}],issues:[{severity:'unknown',resource:'service/guest',field:'native_state',code:'backup_native_dependencies',message:'Native guest dependencies have not been enumerated.',remedy:'Inventory instance and attached volume exports before backup.'}]}),
  managed_storage_plan: args => ({storage:args.storage,revision:args.revision,path:'/tmp/new-data',kind:args.storage === 'vm-root' ? 'file' : 'directory',sizeGiB:args.sizeGiB,format:args.storage === 'vm-root' ? 'qcow2' : undefined,...(args.storage === 'vm-root' ? {source:{path:'assets/base.qcow2',format:'qcow2',sha256:'e'.repeat(64)},sourceVirtualBytes:1073741824} : {}),availableBytes:10737418240,uid:1000,gid:1000,mode:'0700',consumers:[],reviewToken:'d'.repeat(64)}),
  managed_config_history: () => ({entries:[{revision:'c'.repeat(64),modifiedAt:new Date().toISOString(),valid:true}]}),
  managed_config_history_read: args => ({revision:args.target,content:'version: 1\n',valid:true}),
  managed_config_revert_plan: args => ({plan:{baseRevision:args.revision,desiredRevision:args.target,changes:[{resource:'service/website',backend:'podman/quadlet',operation:'update'}],files:{issues:[]}}}),
  managed_config_readiness: () => ({ configured: true, valid: readinessScenario !== 'invalid', ...(readinessScenario === 'invalid' ? {error:'Configuration is invalid or inaccessible; open Configuration to repair it.'} : {health: {checkedAt:new Date().toISOString(), runtimeCheckedAt:new Date().toISOString(), issues: readinessScenario === 'healthy' ? [] : readinessScenario === 'unknown' ? [{severity:'unknown',resource:'service/website',field:'runtime',code:'check_cancelled',message:'Backend checks could not complete.',remedy:'Restore backend access and retry.'}] : readinessScenario === 'warning' ? [{severity:'warning',resource:'storage/vm-root',field:'source.path',code:'initial_asset_unavailable',message:'Initial image asset is unavailable.',remedy:'Restore the asset when provisioning a new disk; the existing disk remains usable.'}] : fixtures.managed_services().health.issues}}) }),
  managed_config_check: () => ({ checkedAt:new Date().toISOString(), issues:[] }),
  managed_config_model: () => ({ revision: 'a'.repeat(64), config: { version: 1, services: {}, shares: {}, storage: { documents: {path:'data/documents',kind:'directory'}, 'vm-root': {path:'data/vm-root.qcow2',kind:'file',format:'qcow2',source:{path:'assets/base.qcow2',format:'qcow2',sha256:'e'.repeat(64)}} }, bindings: { lan:{kind:'network',name:'default'}, pool:{kind:'pool',name:'default'}, project:{kind:'project',name:'default'} }, secrets: {} } }),
  managed_config_read: () => ({ content: 'version: 1\n', revision: 'a'.repeat(64), valid: true }),
  managed_apply_status: () => applyScenario === 'failed' ? {stage:'failed',resources:{'service/website':{state:'failed',message:artifactConflictMessage},'service/database':{state:'registered'}}} : ({ stage: 'complete' }),
  managed_artifact_review: args => ({resource:args.resource,revision:'a'.repeat(64),reviewToken:'e'.repeat(64),canReconcile:true,parts:[{label:'Native Quadlet definitions',files:'desired',manifest:'conflicting'}]}),
  managed_shares: () => ({ configured: true, revision: 'a'.repeat(64), shares: [] }),
  managed_resource_plan: args => ({ baseRevision: args.revision, desiredRevision: 'b'.repeat(64), changes: [{resource: `${args.kind}/${args.id}`,backend:'fixture',operation:args.operation}], files: {issues:[]} }),
  managed_services: () => applicationsScenario === 'partial' ? {configured:true,valid:true,health:{checkedAt:new Date().toISOString(),issues:[]},services:[{id:'website',name:'Website',kind:'container',backend:'podman/compose',enabled:true,state:'partially_running',configuration:'registered',registered:true,members:[{name:'web',state:'running'},{name:'worker',state:'not_running'}],issues:[{severity:'warning',resource:'service/website',field:'runtime',code:'partial_container_group',message:'1 of 2 configured containers are running.',remedy:'Use Start to start missing members, or Stop to stop the application.'}]}]} : applicationsScenario === 'empty' ? {configured:true,valid:true,health:{checkedAt:new Date().toISOString(),issues:[]},services:[]} : ({ configured: true, valid: true, health: { checkedAt: new Date().toISOString(), issues: [{severity: 'error', resource: 'service/website', field: 'path', code: 'missing_path', message: 'A required folder is missing.', remedy: 'Restore the data folder before starting.'}] }, services: [{ id: 'website', name: 'Website', kind: 'container', backend: 'podman/quadlet', enabled: true, state: 'unknown', configuration: 'pending', registered: false, issues: [{severity: 'error', resource: 'service/website', field: 'path', code: 'missing_path', message: 'A required folder is missing.', remedy: 'Restore the data folder before starting.'}] }] }),
  services_list: args => ({ scope: args.scope ?? 'system', available: true, journalAvailable: true, services: [{ name: 'sshd.service', description: 'OpenSSH server', load: 'loaded', active: 'active', sub: 'running', enabled: 'enabled' }], warnings: [] }),
  services_details: () => ({ name: 'sshd.service', description: 'OpenSSH server', load: 'loaded', active: 'active', sub: 'running', enabled: 'enabled', canStart: false, canStop: true, canReload: true, mainPID: 1234 }),
  shares_list: () => ({ backends: [{ protocol: 'samba', available: true, canManage: true, managedFile: '/tmp/server-man/samba.conf', revision: 'a'.repeat(64), setupRequired: false }, { protocol: 'nfs', available: true, canManage: true, managedFile: '/tmp/server-man/exports', revision: 'b'.repeat(64), setupRequired: false }], shares: [], warnings: [] }),
  updates_status: () => ({ manager: { id: 'apt', title: 'APT', available: true, canRefresh: true }, distribution: 'Ubuntu 24.04', packages: [{ name: 'demo-package', architecture: 'amd64', installed: '1.0', candidate: '1.1', kind: 'upgrade' }], valid: true, notice: 'Fixture package candidates.', cacheSource: 'fixture cache', reboot: { required: false, reason: 'No marker' }, readAt: new Date().toISOString() }),
}

function fixtureFor(tool, args) {
  const factory = fixtures[tool]
  if (factory) return factory(args)
  if (tool === 'services_logs') return { text: 'fixture journal', truncated: false }
  if (tool === 'managed_service_logs') return { text: 'fixture managed application log', truncated: false, source: 'podman-compose' }
  if (tool.startsWith('jobs_')) return null
  managementCalls.push({ tool, args })
  return { job: { id: `fixture-${managementCalls.length}`, tool, state: 'running', createdAt: new Date().toISOString(), output: '', truncated: false } }
}

async function exerciseManagement(page) {
  fixtureMode = true
  await page.reload()
  await page.getByRole('heading', { name: 'Overview', exact: true }).waitFor()
  await page.getByText('Management enabled', { exact: true }).waitFor()

  // A unified configuration exists before any workload is defined. This is
  // an empty installation rather than a failed search.
  applicationsScenario = 'empty'
  await page.getByRole('navigation').getByRole('button', {name:'Applications',exact:true}).click()
  await page.getByRole('heading', {name:'No managed applications yet',exact:true}).waitFor()
  assert.equal(await page.getByRole('heading', {name:'No matching applications',exact:true}).count(),0)
  assert.equal(await page.getByRole('button', {name:'Define application',exact:true}).isEnabled(),true)
  applicationsScenario = 'partial'
  await page.getByRole('button', {name:'Check configuration',exact:true}).click()
  await page.getByText('partially running',{exact:true}).waitFor()
  await page.getByText('1 of 2 configured containers are running.',{exact:true}).waitFor()
  await page.getByText('Containers (2)',{exact:true}).click()
  await page.getByText('worker', {exact:true}).waitFor()
  assert.equal(await page.getByRole('button',{name:'Start',exact:true}).isEnabled(),true)
  assert.equal(await page.getByRole('button',{name:'Stop',exact:true}).isEnabled(),true)
  await page.getByRole('button',{name:'Read logs',exact:true}).click()
  await page.getByText('fixture managed application log',{exact:true}).waitFor()
  applicationsScenario = 'blocked'
  await page.getByRole('navigation').getByRole('button', {name:'Overview',exact:true}).click()

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
  await page.getByRole('searchbox', {name:'Find an application',exact:true}).fill('no-such-application')
  await page.getByRole('heading', {name:'No matching applications',exact:true}).waitFor()
  assert.equal(await page.getByRole('heading', {name:'No managed applications yet',exact:true}).count(),0)
  await page.getByRole('searchbox', {name:'Find an application',exact:true}).fill('')
  assert.equal(await page.locator('body').evaluate(el => el.scrollWidth <= window.innerWidth), true, 'application diagnostics overflow')
  await page.getByRole('button',{name:'Define application',exact:true}).click()
  const applicationEditor = page.getByRole('region',{name:'Definition editor'})
  await applicationEditor.getByLabel('Resource ID',{exact:true}).fill('fixture-app')
  await applicationEditor.getByLabel('Display name',{exact:true}).fill('Fixture application')
  await applicationEditor.getByLabel('Application type',{exact:true}).selectOption('vm')
  await applicationEditor.getByLabel('VM backend',{exact:true}).selectOption('incus')
  await applicationEditor.getByText('Incus v1: Stop the VM before changing CPU, memory, storage, network, firmware or native description. Save for next start does not defer running hardware edits. Start at boot can be changed without stopping the VM.',{exact:true}).waitFor()
  await applicationEditor.getByLabel('Application type',{exact:true}).selectOption('container')
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
  await page.evaluate(({ id, tool }) => window.dispatchEvent(new CustomEvent('server-man:job-finished', { detail: { id, tool, state: 'succeeded' } })), { id: `fixture-${managementCalls.length}`, tool: 'managed_resource_save' })
  await page.getByRole('button', { name: 'Set up server…', exact: true }).click()
  await page.getByRole('dialog').getByRole('button', { name: 'Set up server', exact: true }).click()
  await page.locator('.activity-button').click()


  await page.getByRole('navigation').getByRole('button', { name: 'Updates' }).click()
  await page.getByRole('textbox', { name: 'Install repository packages' }).fill('samba nfs-utils')
  await page.getByRole('button', { name: 'Install packages…', exact: true }).click()
  await page.getByRole('dialog').getByRole('button', { name: 'Install packages', exact: true }).click()
  await page.locator('.activity-button').click()
  await page.evaluate(({ id, tool }) => window.dispatchEvent(new CustomEvent('server-man:job-finished', { detail: { id, tool, state: 'succeeded' } })), { id: `fixture-${managementCalls.length}`, tool: 'packages_install' })
  await page.getByLabel('Module dependencies', { exact: true }).selectOption('podman')
  await page.getByRole('button', { name: 'Install dependencies…', exact: true }).click()
  await page.getByRole('dialog').getByRole('button', { name: 'Install dependencies', exact: true }).click()
  await page.locator('.activity-button').click()
  await page.evaluate(({ id, tool }) => window.dispatchEvent(new CustomEvent('server-man:job-finished', { detail: { id, tool, state: 'succeeded' } })), { id: `fixture-${managementCalls.length}`, tool: 'dependencies_install' })
  await page.getByRole('button', { name: 'Install system updates' }).click()
  const updateDialog = page.getByRole('dialog')
  await updateDialog.getByRole('heading', { name: 'Install system package updates' }).waitFor()
  await updateDialog.getByRole('button', { name: 'Install updates' }).click()
  await page.getByText('System update started. Follow its progress in Activity.', { exact: true }).waitFor()
  await page.locator('.activity-button').click()

  await page.getByRole('navigation').getByRole('button',{name:'Configuration',exact:true}).click()
  applyScenario = 'failed'
  const retriesBefore = managementCalls.filter(call=>call.tool==='managed_apply_retry').length
  await page.getByRole('button',{name:'Refresh configuration',exact:true}).click()
  const applyRecovery = page.getByRole('region',{name:'Configuration apply recovery',exact:true})
  await applyRecovery.getByText(artifactConflictMessage,{exact:true}).waitFor()
  await applyRecovery.getByText('service/database',{exact:true}).waitFor()
  await page.getByRole('navigation').getByRole('button',{name:'Applications',exact:true}).click()
  await page.getByRole('navigation').getByRole('button',{name:'Configuration',exact:true}).click()
  await applyRecovery.getByText(artifactConflictMessage,{exact:true}).waitFor()
  assert.equal(managementCalls.filter(call=>call.tool==='managed_apply_retry').length,retriesBefore,'viewing durable failures must not retry them')
  await applyRecovery.getByRole('button',{name:'Review generated artifact recovery for service/website',exact:true}).click()
  const artifactReview = applyRecovery.getByRole('region',{name:'Generated artifact metadata review',exact:true})
  await artifactReview.getByText('Native files: desired; publication manifest: conflicting',{exact:true}).waitFor()
  assert.equal(managementCalls.filter(call=>call.tool==='managed_artifact_reconcile').length,0,'read-only artifact review mutated native metadata')
  await artifactReview.getByRole('button',{name:'Confirm metadata-only reconciliation',exact:true}).click()
  assert.deepEqual(managementCalls.at(-1),{tool:'managed_artifact_reconcile',args:{resource:'service/website',revision:'a'.repeat(64),reviewToken:'e'.repeat(64),confirm:true}})
  await page.getByText('Metadata-only repair started. Inspect the result before explicitly retrying apply.',{exact:true}).waitFor()
  await page.evaluate(id=>window.dispatchEvent(new CustomEvent('server-man:job-finished',{detail:{id,tool:'managed_artifact_reconcile',state:'succeeded'}})),`fixture-${managementCalls.length}`)
  await applyRecovery.getByText(artifactConflictMessage,{exact:true}).waitFor()
  await applyRecovery.getByRole('button',{name:'Inspect and retry apply',exact:true}).click()
  await page.getByText('Recovery inspection and retry started.',{exact:true}).waitFor()
  assert.deepEqual(managementCalls.at(-1),{tool:'managed_apply_retry',args:{confirm:true}})
  await page.locator('.activity-button').click()
  await page.evaluate(id=>window.dispatchEvent(new CustomEvent('server-man:job-finished',{detail:{id,tool:'managed_apply_retry',state:'failed',error:'Artifact conflict persists.'}})),`fixture-${managementCalls.length}`)
  await applyRecovery.getByText(artifactConflictMessage,{exact:true}).waitFor()
  applyScenario = 'complete'
  await page.getByRole('button',{name:'Refresh configuration',exact:true}).click()
  await applyRecovery.waitFor({state:'detached'})
  await exerciseBackupInventory(page)
  await page.getByRole('button',{name:'Create empty data for documents',exact:true}).click()
  const provisioning = page.getByRole('region',{name:'New storage provisioning',exact:true})
  await provisioning.getByRole('button',{name:'Review new data creation',exact:true}).click()
  await provisioning.getByRole('heading',{name:'Review new storage',exact:true}).waitFor()
  assert.equal(await provisioning.getByRole('button',{name:'Confirm new data creation',exact:true}).isDisabled(),true)
  await provisioning.getByRole('checkbox',{name:'I want new empty data; I am not restoring missing data.',exact:true}).check()
  await provisioning.getByRole('button',{name:'Confirm new data creation',exact:true}).click()
  await page.getByText('New data creation started. Follow Activity for results; workloads remain stopped.',{exact:true}).waitFor()
  await page.locator('.activity-button').click()
  const provisioned = managementCalls.find(call => call.tool === 'managed_storage_create')
  assert.deepEqual(provisioned.args,{storage:'documents',revision:'a'.repeat(64),sizeGiB:0,reviewToken:'d'.repeat(64),purpose:'new_data',confirm:true})
  await page.evaluate(({id,tool}) => window.dispatchEvent(new CustomEvent('server-man:job-finished',{detail:{id,tool,state:'succeeded'}})),{id:`fixture-${managementCalls.length}`,tool:'managed_storage_create'})
  await page.getByRole('button',{name:'Add storage',exact:true}).click()
  const diskEditor = page.getByRole('region',{name:'Definition editor',exact:true})
  await diskEditor.getByLabel('Resource ID',{exact:true}).fill('fixture-disk')
  await diskEditor.getByLabel('Data path',{exact:true}).fill('data/new-vm.qcow2')
  await diskEditor.getByLabel('Data type',{exact:true}).selectOption('file')
  await diskEditor.getByLabel('Disk format',{exact:true}).selectOption('qcow2')
  await diskEditor.getByRole('checkbox',{name:'Provision a new disk from a local image',exact:true}).check()
  await diskEditor.getByLabel('Initial image asset path',{exact:true}).fill('assets/base.qcow2')
  await diskEditor.getByLabel('Initial image SHA-256',{exact:true}).fill('e'.repeat(64))
  assert.equal(await diskEditor.getByLabel('Initial image SHA-256',{exact:true}).getAttribute('pattern'),'[0-9a-f]{64}')
  await diskEditor.getByRole('button',{name:'Review change',exact:true}).click()
  await diskEditor.getByRole('heading',{name:'Review changes',exact:true}).waitFor()
  await diskEditor.getByRole('button',{name:'Confirm and save',exact:true}).click()
  await page.locator('.activity-button').click()
  const imageDefinition = JSON.parse(managementCalls.find(call => call.tool === 'managed_resource_save' && call.args.id === 'fixture-disk').args.definition)
  assert.deepEqual(imageDefinition.source,{path:'assets/base.qcow2',format:'qcow2',sha256:'e'.repeat(64)})
  await page.evaluate(({id,tool}) => window.dispatchEvent(new CustomEvent('server-man:job-finished',{detail:{id,tool,state:'succeeded'}})),{id:`fixture-${managementCalls.length}`,tool:'managed_resource_save'})
  await page.getByRole('button',{name:'Provision image for vm-root',exact:true}).click()
  const imageProvision = page.getByRole('region',{name:'New storage provisioning',exact:true})
  await imageProvision.getByRole('button',{name:'Review new data creation',exact:true}).click()
  await imageProvision.getByText(/Initial image: assets\/base.qcow2/).waitFor()
  await imageProvision.getByRole('checkbox',{name:'I want a new disk from this image; I am not restoring missing guest data.',exact:true}).check()
  await imageProvision.getByRole('button',{name:'Confirm new data creation',exact:true}).click()
  await page.locator('.activity-button').click()
  const cloned = managementCalls.find(call => call.tool === 'managed_storage_create' && call.args.storage === 'vm-root')
  assert.equal(cloned.args.sizeGiB,20)
  assert.equal(cloned.args.purpose,'new_data')
  await page.evaluate(({id,tool}) => window.dispatchEvent(new CustomEvent('server-man:job-finished',{detail:{id,tool,state:'succeeded'}})),{id:`fixture-${managementCalls.length}`,tool:'managed_storage_create'})
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
  assert.deepEqual(JSON.parse(managementCalls.find(call => call.tool === 'managed_resource_save' && call.args.kind === 'share').args.definition).users, ['alice'])
  assert.equal(submitted.updates_install?.confirm, true)
  assert.equal(submitted.services_stop?.scope, 'system')
  assert.equal(submitted.packages_install?.packages, 'samba nfs-utils')
  assert.equal(submitted.packages_install?.confirm, true)
  assert.equal(submitted.shares_setup?.protocol, 'samba')
  assert.equal(submitted.shares_setup?.confirm, true)
  assert.equal(submitted.dependencies_install?.target, 'podman')
  assert.equal(submitted.dependencies_install?.confirm, true)
  assert.deepEqual(deniedByServer.sort(), ['managed_backup_prepare', 'managed_backup_prepare', 'managed_backup_recover', 'managed_backup_release', 'managed_backup_release', 'managed_backup_publish', 'managed_resource_save', 'managed_resource_save', 'managed_resource_save', 'managed_config_revert', 'managed_storage_create', 'managed_storage_create', 'services_stop', 'updates_install', 'packages_install', 'shares_setup', 'dependencies_install'].sort())
  // The alert stays mounted on every page; rechecks clear only resolved issues.
  const beforeRepairCalls = managementCalls.length
  readinessScenario = 'unknown'
  await globalHealth.getByRole('button',{name:'Recheck requirements',exact:true}).click()
  await globalHealth.getByText('Configuration readiness is uncertain',{exact:true}).waitFor()
  readinessScenario = 'warning'
  await globalHealth.getByRole('button',{name:'Recheck requirements',exact:true}).click()
  await globalHealth.getByText('Configuration warnings',{exact:true}).waitFor()
  await globalHealth.getByText('Warnings alone do not prevent workloads from starting.',{exact:false}).waitFor()
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

async function exerciseBackupInventory(page) {
  const calls = managementCalls.length
  const inventory = page.getByRole('region',{name:'Backup inventory',exact:true})
  await inventory.getByRole('button',{name:'Review backup inventory',exact:true}).click()
  await inventory.getByText(/Stage: inventory/).waitFor()
  await inventory.getByText('Inventory instance and attached volume exports before backup.',{exact:true}).waitFor()
  await inventory.getByRole('heading',{name:'Capture backup inputs',exact:true}).waitFor()
  await inventory.getByText('storage/documents',{exact:true}).waitFor()
  await inventory.getByText('Native object: incus · sm-guest · project managed · pool default',{exact:true}).waitFor()
  await inventory.getByText('Observed state: stopped',{exact:true}).waitFor()
  assert.equal(managementCalls.length,calls,'backup inventory must not mutate workloads or create a bundle')
  await inventory.getByLabel('Destination directory (absolute path)',{exact:true}).fill('/tmp/server-man-ui-capture')
  await inventory.getByLabel('I acknowledge this writes private backup inputs and may stop running workloads',{exact:true}).check()
  await inventory.getByRole('button',{name:'Capture backup inputs',exact:true}).click()
  await inventory.getByRole('button',{name:'Capturing inputs…',exact:true}).waitFor()
  assert.deepEqual(managementCalls.at(-1),{tool:'managed_backup_prepare',args:{revision:'a'.repeat(64),destination:'/tmp/server-man-ui-capture',quiesce:true,confirm:true}})
  assert.equal(await inventory.getByText('Backup inputs captured. Restore readiness remains unverified.',{exact:true}).count(),0,'submission must not be shown as completion')
  await page.evaluate(id=>window.dispatchEvent(new CustomEvent('server-man:job-finished',{detail:{id,tool:'managed_backup_prepare',state:'failed',error:'Fixture: guest needs recovery'}})),`fixture-${managementCalls.length}`)
  await inventory.getByText('Fixture: guest needs recovery',{exact:true}).waitFor()
  await inventory.getByRole('button',{name:'Capture backup inputs',exact:true}).click()
  await inventory.getByRole('button',{name:'Capturing inputs…',exact:true}).waitFor()
  const completedID=`fixture-${managementCalls.length}`
  backupJobResults.set(completedID,{id:completedID,tool:'managed_backup_prepare',state:'succeeded',result:{stage:'captured',destination:'/tmp/server-man-ui-capture',revision:'a'.repeat(64),preparedAt:new Date().toISOString(),totalEntries:3,totalBytes:123,manifestChecksum:'b'.repeat(64),quiesced:true,resumedServices:['app']}})
  // Production list events omit results; the component must fetch jobs_get.
  await page.evaluate(id=>window.dispatchEvent(new CustomEvent('server-man:job-finished',{detail:{id,tool:'managed_backup_prepare',state:'succeeded'}})),completedID)
  await inventory.getByText('Backup inputs captured. Restore readiness remains unverified.',{exact:true}).waitFor()

  backupRecoveryPending=true
  const recovery=inventory.getByRole('region',{name:'Backup recovery',exact:true})
  await recovery.getByRole('button',{name:'Review recovery',exact:true}).click()
  await recovery.getByText('A backup capture was interrupted or workloads still need recovery. Managed changes are blocked until recovery completes.',{exact:true}).waitFor()
  await recovery.getByText('app: failed',{exact:true}).waitFor()
  await recovery.getByText('guest: stop uncertain',{exact:true}).waitFor()
  await recovery.getByLabel('I reviewed the interrupted capture and selected a recovery action',{exact:true}).check()
  await recovery.getByRole('button',{name:'Resume backup workloads',exact:true}).click()
  await recovery.getByRole('button',{name:'Recovering workloads…',exact:true}).waitFor()
  assert.deepEqual(managementCalls.at(-1),{tool:'managed_backup_recover',args:{revision:'a'.repeat(64),reviewToken:'c'.repeat(64),confirm:true}})
  backupRecoveryPending=false
  await page.evaluate(id=>window.dispatchEvent(new CustomEvent('server-man:job-finished',{detail:{id,tool:'managed_backup_recover',state:'succeeded'}})),`fixture-${managementCalls.length}`)
  await recovery.getByText('No pending backup recovery.',{exact:true}).waitFor()
  backupRecoveryPending=true;backupRecoveryCurrentRevision='d'.repeat(64)
  await recovery.getByRole('button',{name:'Review recovery',exact:true}).click()
  await recovery.getByText('app: failed',{exact:true}).waitFor()
  assert.equal(await recovery.getByRole('button',{name:'Leave workloads as they are',exact:true}).isDisabled(),true)
  await recovery.getByLabel('I reviewed the interrupted capture and selected a recovery action',{exact:true}).check()
  await recovery.getByRole('button',{name:'Leave workloads as they are',exact:true}).click()
  await recovery.getByRole('button',{name:'Releasing recovery…',exact:true}).waitFor()
  assert.deepEqual(managementCalls.at(-1),{tool:'managed_backup_release',args:{revision:'d'.repeat(64),reviewToken:'c'.repeat(64),confirm:true}})
  backupRecoveryPending=false;backupRecoveryCurrentRevision='a'.repeat(64)
  await page.evaluate(id=>window.dispatchEvent(new CustomEvent('server-man:job-finished',{detail:{id,tool:'managed_backup_release',state:'succeeded'}})),`fixture-${managementCalls.length}`)
  await recovery.getByText('No pending backup recovery.',{exact:true}).waitFor()
  backupPublicationPending=true
  await recovery.getByRole('button',{name:'Review recovery',exact:true}).click()
  await recovery.getByText('Recorded publication phase: publishing',{exact:true}).waitFor()
  await recovery.getByText('Recorded staging location: /tmp/.staging-backup-fixture',{exact:true}).waitFor()
  assert.equal(await recovery.getByRole('button',{name:'Resume backup workloads',exact:true}).count(),0,'publication-only review must not offer workload replay')
  assert.equal(await recovery.getByRole('button',{name:'Verify and finish publication',exact:true}).isDisabled(),true)
  await recovery.getByLabel('I reviewed the interrupted capture and selected a recovery action',{exact:true}).check()
  await recovery.getByRole('button',{name:'Verify and finish publication',exact:true}).click()
  await recovery.getByRole('button',{name:'Verifying publication…',exact:true}).waitFor()
  assert.deepEqual(managementCalls.at(-1),{tool:'managed_backup_publish',args:{revision:'a'.repeat(64),reviewToken:'c'.repeat(64),confirm:true}})
  backupPublicationPending=false
  await page.evaluate(id=>window.dispatchEvent(new CustomEvent('server-man:job-finished',{detail:{id,tool:'managed_backup_publish',state:'succeeded'}})),`fixture-${managementCalls.length}`)
  await recovery.getByText('No pending backup recovery.',{exact:true}).waitFor()
  backupPublicationPending=true
  backupPublicationFinalized=false
  await recovery.getByRole('button',{name:'Review recovery',exact:true}).click()
  await recovery.getByText('The interrupted capture has no finalized manifest checksum. Inspect the incomplete data before releasing recovery.',{exact:true}).waitFor()
  assert.equal(await recovery.getByRole('button',{name:'Verify and finish publication',exact:true}).count(),0,'incomplete captures must not offer publication')
  backupPublicationFinalized=true
  await recovery.getByRole('button',{name:'Review recovery',exact:true}).click()
  await recovery.getByLabel('I reviewed the interrupted capture and selected a recovery action',{exact:true}).check()
  await recovery.getByRole('button',{name:'Leave workloads as they are',exact:true}).click()
  await recovery.getByRole('button',{name:'Releasing recovery…',exact:true}).waitFor()
  assert.deepEqual(managementCalls.at(-1),{tool:'managed_backup_release',args:{revision:'a'.repeat(64),reviewToken:'c'.repeat(64),confirm:true}})
  backupPublicationPending=false
  await page.evaluate(id=>window.dispatchEvent(new CustomEvent('server-man:job-finished',{detail:{id,tool:'managed_backup_release',state:'succeeded'}})),`fixture-${managementCalls.length}`)
  await recovery.getByText('No pending backup recovery.',{exact:true}).waitFor()
  assert.equal(await page.locator('body').evaluate(el => el.scrollWidth <= window.innerWidth),true,'backup inventory overflows')
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
        if (tool === 'jobs_get' && backupJobResults.has(args.id)) {
          await route.fulfill({status:200,contentType:'application/json',body:JSON.stringify(backupJobResults.get(args.id))})
          return
        }
        if (tool.startsWith('jobs_')) {
          const reply = await send({ method: request.method(), url: request.url(), headers: request.headers(), body: request.postData() ?? '' })
          await route.fulfill({ status: reply.status, headers: { 'content-type': 'application/json' }, body: Buffer.from(reply.body, 'base64') })
          return
        }
        if (['services_stop', 'managed_backup_prepare', 'managed_backup_recover', 'managed_backup_release', 'managed_backup_publish', 'managed_resource_save', 'managed_config_revert', 'managed_storage_create', 'updates_install', 'packages_install', 'shares_setup', 'dependencies_install'].includes(tool)) {
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
      await page.getByRole('button',{name:'Toggle navigation',exact:true}).click()
      await page.getByRole('navigation').getByRole('button',{name:'Configuration',exact:true}).click()
      await exerciseBackupInventory(page)
      fixtureMode = false
    } else {
      fixtureMode = true
      await page.reload()
      await page.getByRole('region',{name:'Configuration readiness',exact:true}).getByText('Workload configuration alert',{exact:true}).waitFor()
      assert.equal(await page.locator('body').evaluate(el => el.scrollWidth <= window.innerWidth),true,'small-mobile configuration alert overflows')
      await page.getByRole('button',{name:'Toggle navigation',exact:true}).click()
      await page.getByRole('navigation').getByRole('button',{name:'Configuration',exact:true}).click()
      await exerciseBackupInventory(page)
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
