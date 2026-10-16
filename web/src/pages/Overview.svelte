<script lang="ts">
  import { onMount } from 'svelte'
  import Icon from '../lib/Icon.svelte'
  import Sparkline from '../lib/Sparkline.svelte'
  import { invokeTool } from '../lib/api'
  import { bytes, percent, rate, uptime, type Snapshot, type ProcessList } from '../lib/monitor'

  let snapshot = $state<Snapshot | null>(null)
  let processes = $state<ProcessList | null>(null)
  let metricsError = $state('')
  let processError = $state('')
  let paused = $state(false)
  let sort = $state('cpu')
  let filter = $state('')
  let rows = $state(50)
  let allCores = $state(false)
  let refresh = $state<() => Promise<void>>(async () => {})
  let searchTimer: ReturnType<typeof setTimeout>
  const latest = $derived(snapshot?.history.at(-1))
  const sampleTimes = $derived(snapshot?.history.map(p => new Date(p.at).getTime()) ?? [])
  const memoryPercent = $derived(snapshot?.memory.total ? snapshot.memory.used / snapshot.memory.total * 100 : 0)
  const stale = $derived(!!snapshot && (metricsError !== '' || snapshot.lastError || Date.now() - new Date(snapshot.at).getTime() > Math.max(10_000, snapshot.intervalSeconds * 5000)))
  const windowLabel = $derived(snapshot ? `${Math.round(snapshot.intervalSeconds * 60 / 60)} min` : '2 min')

  onMount(() => {
    let stopped = false, polling = false
    let timer: ReturnType<typeof setTimeout>
    const controller = new AbortController()
    const schedule = () => { if (!stopped) timer = setTimeout(poll, (snapshot?.intervalSeconds || 2) * 1000) }
    async function poll(manual = false) {
      if (stopped || polling) return
      clearTimeout(timer)
      if ((!manual && paused) || document.hidden) { schedule(); return }
      polling = true
      try {
        const results = await Promise.allSettled([
          invokeTool<Snapshot>('monitor_snapshot', {}, controller.signal),
          invokeTool<ProcessList>('monitor_processes', { sort, filter, limit: rows }, controller.signal),
        ])
        if (stopped) return
        if (results[0].status === 'fulfilled') { snapshot = results[0].value; metricsError = '' }
        else metricsError = results[0].reason instanceof Error ? results[0].reason.message : 'Could not read resources'
        if (results[1].status === 'fulfilled') { processes = results[1].value; processError = '' }
        else processError = results[1].reason instanceof Error ? results[1].reason.message : 'Could not read processes'
      } finally { polling = false; schedule() }
    }
    refresh = () => poll(true)
    const visible = () => { if (!document.hidden && !paused) void poll() }
    document.addEventListener('visibilitychange', visible)
    void poll()
    return () => { stopped = true; clearTimeout(timer); clearTimeout(searchTimer); controller.abort(); document.removeEventListener('visibilitychange', visible) }
  })

  function changeSort(value: string) { sort = value; void refresh() }
  function search() { clearTimeout(searchTimer); searchTimer = setTimeout(() => { void refresh() }, 250) }
</script>

{#if metricsError || snapshot?.lastError}
  <div class="notice error monitor-notice" role="alert"><div><strong>Resource data could not be updated</strong><p>{metricsError || snapshot?.lastError}</p>{#if snapshot?.valid}<p>Showing the last successful sample.</p>{/if}</div><button onclick={refresh}>Retry</button></div>
{/if}
{#if snapshot?.valid}
  <div class="monitor-strip">
    <div class="system-identity"><Icon name="server" size={17} /><span>{snapshot.host.os}<span class="muted"> · {snapshot.host.architecture}</span></span></div>
    <div class="monitor-controls"><span class="sample-status"><span class="connection-dot" class:connected={!paused && !stale}></span>{paused ? 'Paused' : stale ? 'Stale data' : `Live · ${snapshot.intervalSeconds}s`}</span><button class="small-button" aria-pressed={paused} onclick={() => { paused = !paused; if (!paused) void refresh() }}>{paused ? 'Resume' : 'Pause'}</button></div>
  </div>
  {#if snapshot.warnings.length}<details class="monitor-warnings"><summary>{snapshot.warnings.length} monitoring {snapshot.warnings.length === 1 ? 'notice' : 'notices'}</summary><ul>{#each snapshot.warnings as warning}<li>{warning}</li>{/each}</ul></details>{/if}
  <div class="monitor-grid">
    <section class="panel resource-card cpu-card" aria-labelledby="cpu-heading">
      <div class="resource-heading"><h2 id="cpu-heading"><Icon name="cpu" size={17} />Processor</h2><span>{snapshot.cpu.cores.length} logical cores</span></div>
      <div class="cpu-overview"><div class="resource-value">{snapshot.cpu.usage?.toFixed(1) ?? '—'}<span>{snapshot.cpu.usage == null ? '' : '%'}</span></div><div class="cpu-context"><span>{snapshot.cpu.frequencyMHz ? `${(snapshot.cpu.frequencyMHz / 1000).toFixed(2)} GHz` : 'Frequency unavailable'}</span><span>{snapshot.temperatures.length ? `${Math.max(...snapshot.temperatures.map(t => t.celsius)).toFixed(0)}°C max sensor` : 'Temperature not exposed'}</span></div></div>
      <div class="chart-wrap"><Sparkline values={snapshot.history.map(p => p.cpu)} times={sampleTimes} windowSeconds={snapshot.intervalSeconds * 60} ceiling={100} label={`CPU usage over the last ${windowLabel}`} />{#if snapshot.cpu.usage == null}<span class="chart-waiting">Waiting for the next sample</span>{/if}</div>
      <div class="chart-axis"><span>{windowLabel} ago</span><span>now</span></div>
      <div class="cpu-model">{snapshot.host.cpuModel}</div>
      <div class="core-grid">{#each snapshot.cpu.cores.slice(0, allCores ? undefined : 16) as core}<div class="core-cell"><div><span>{core.name}</span><strong>{percent(core.usage)}</strong></div><div class="meter-track" aria-hidden="true"><span style:width={`${core.usage ?? 0}%`}></span></div></div>{/each}</div>
      {#if snapshot.cpu.cores.length > 16}<button class="text-button" onclick={() => { allCores = !allCores }}>{allCores ? 'Show fewer cores' : `Show all ${snapshot.cpu.cores.length} cores`}</button>{/if}
    </section>

    <section class="panel resource-card memory-card" aria-labelledby="memory-heading">
      <div class="resource-heading"><h2 id="memory-heading"><Icon name="memory" size={17} />Memory</h2><span>{percent(memoryPercent)} used</span></div>
      <div class="resource-value memory-value">{bytes(snapshot.memory.used)}<span> / {bytes(snapshot.memory.total)}</span></div>
      <div class="memory-bar" aria-hidden="true"><span style:width={`${memoryPercent}%`}></span></div>
      <dl class="resource-stats"><div><dt>Available</dt><dd>{bytes(snapshot.memory.available)}</dd></div><div><dt>Cache</dt><dd>{bytes(snapshot.memory.cached)}</dd></div><div><dt>Buffers</dt><dd>{bytes(snapshot.memory.buffers)}</dd></div></dl>
      <div class="swap-section"><div class="resource-heading"><h3>Swap</h3><span>{snapshot.memory.swapTotal ? `${bytes(snapshot.memory.swapUsed)} / ${bytes(snapshot.memory.swapTotal)}` : 'Not configured'}</span></div><div class="meter-track swap-track" aria-hidden="true"><span style:width={`${snapshot.memory.swapTotal ? snapshot.memory.swapUsed / snapshot.memory.swapTotal * 100 : 0}%`}></span></div></div>
      <dl class="host-stats"><div><dt>Uptime</dt><dd>{uptime(snapshot.uptimeSeconds)}</dd></div><div><dt>Load <span>1 / 5 / 15 min</span></dt><dd>{snapshot.load?.map(n => n.toFixed(2)).join(' / ') ?? '—'}</dd></div><div><dt>Kernel</dt><dd title={snapshot.host.kernel}>{snapshot.host.kernel || '—'}</dd></div></dl>
    </section>

    <section class="panel resource-card network-card" aria-labelledby="network-heading">
      <div class="resource-heading"><h2 id="network-heading"><Icon name="network" size={17} />Network</h2><span>Except loopback</span></div>
      <div class="network-values"><div><span class="metric-label"><span class="legend-dot"></span>RECEIVING</span><strong>{rate(latest?.rx)}</strong></div><div><span class="metric-label"><span class="legend-dot blue"></span>SENDING</span><strong>{rate(latest?.tx)}</strong></div></div>
      <Sparkline values={snapshot.history.map(p => p.rx)} times={sampleTimes} windowSeconds={snapshot.intervalSeconds * 60} secondary={snapshot.history.map(p => p.tx)} label={`Receive and send rates over the last ${windowLabel}, excluding loopback`} />
      <div class="chart-axis"><span>{windowLabel} ago</span><span>now</span></div>
      <div class="interface-list">{#each snapshot.networks as network}<div class="interface-row"><span class="mono">{network.name}{#if network.name === 'lo'}<small>loopback</small>{/if}</span><span><span class="receive-label">↓</span> {rate(network.rxBytesPerSecond)}</span><span><span class="send-label">↑</span> {rate(network.txBytesPerSecond)}</span></div>{/each}</div>
      {#if !snapshot.networks.some(n => n.name !== 'lo')}<p class="resource-hint">No external network interfaces are visible to the server.</p>{/if}
    </section>

    <section class="panel resource-card storage-card" aria-labelledby="storage-heading">
      <div class="resource-heading"><h2 id="storage-heading"><Icon name="drive" size={17} />Storage</h2><span>Local filesystems</span></div>
      <div class="disk-list">{#each snapshot.disks as disk}<div class="disk-item"><div class="disk-title"><strong>{disk.mount}</strong><span>{percent(disk.total ? disk.used / disk.total * 100 : null)}</span></div><div class="disk-meta"><span title={disk.device}>{disk.device} · {disk.filesystem}</span><span>{bytes(disk.used)} / {bytes(disk.total)}</span></div><div class="meter-track disk-track" aria-hidden="true"><span class:high={disk.total > 0 && disk.used / disk.total > .9} style:width={`${disk.total ? disk.used / disk.total * 100 : 0}%`}></span></div></div>{/each}</div>
      {#if !snapshot.disks.length}<p class="resource-hint">No local filesystem usage is available.</p>{/if}
      {#if snapshot.diskIO.length}<div class="disk-io-heading"><span>DEVICE</span><span>READ / WRITE</span></div><div class="disk-io-list">{#each snapshot.diskIO as disk}<div><span class="mono">{disk.name}</span><span>{rate(disk.readBytesPerSecond)}<span class="muted"> / </span>{rate(disk.writeBytesPerSecond)}</span></div>{/each}</div>{/if}
    </section>

    <section class="panel process-card" aria-labelledby="process-heading">
      <div class="process-header"><div class="resource-heading"><h2 id="process-heading"><Icon name="services" size={17} />Processes</h2><span>{snapshot.processCount}{snapshot.processesLimited ? '+' : ''} visible</span></div><div class="process-controls"><label class="sr-only" for="process-search">Search processes</label><input id="process-search" type="search" bind:value={filter} oninput={search} placeholder="Search name or PID…" /><label class="sr-only" for="process-sort">Sort processes</label><select id="process-sort" bind:value={sort} onchange={() => { void refresh() }}><option value="cpu">CPU usage</option><option value="memory">Memory</option><option value="pid">PID</option><option value="name">Name</option></select><label class="sr-only" for="process-rows">Process rows</label><select id="process-rows" bind:value={rows} onchange={() => { void refresh() }}>{#each [25, 50, 100, 250] as value}<option value={value}>{value} rows</option>{/each}</select></div></div>
      {#if processError}<p class="form-error process-error" role="alert">{processError}</p>{/if}
      <div class="process-table-wrap"><table class="process-table"><thead><tr><th class="pid-column" aria-sort={sort === 'pid' ? 'ascending' : 'none'}><button onclick={() => changeSort('pid')}>PID</button></th><th aria-sort={sort === 'name' ? 'ascending' : 'none'}><button onclick={() => changeSort('name')}>NAME</button></th><th class="state-column">STATE</th><th class="numeric" aria-sort={sort === 'cpu' ? 'descending' : 'none'}><button onclick={() => changeSort('cpu')}>CPU {sort === 'cpu' ? '↓' : ''}</button></th><th class="numeric" aria-sort={sort === 'memory' ? 'descending' : 'none'}><button onclick={() => changeSort('memory')}>MEMORY {sort === 'memory' ? '↓' : ''}</button></th></tr></thead><tbody>{#each processes?.processes ?? [] as process}<tr><td class="mono pid-column">{process.pid}</td><td class="process-name"><strong title={process.name}>{process.name}</strong><span class="mobile-pid mono">PID {process.pid}</span></td><td class="state-column"><span class:running={process.state === 'R'} class="process-state">{process.state}</span></td><td class="numeric mono cpu-number">{percent(process.cpu)}</td><td class="numeric mono">{bytes(process.memory)}</td></tr>{/each}</tbody></table></div>
      {#if processes && !processes.processes.length}<p class="process-empty">{filter ? 'No processes match your search.' : 'No processes can be read with the current permissions.'}</p>{/if}
      <div class="process-footer"><span>{processes?.processes.length ?? 0} of {processes?.matched ?? 0}{processes?.limited ? '+' : ''} matches</span><span>100% CPU = one core · names only</span></div>
    </section>
  </div>
  <div class="monitor-caption"><span>Updated {new Date(snapshot.at).toLocaleTimeString()}</span><span>Metrics reflect resources visible to the server process.</span></div>
{:else if !metricsError && !snapshot?.lastError}
  <section class="panel empty-state" aria-busy="true"><span class="loading-ring"></span><h2>Reading system resources</h2><p>Collecting the first Linux sample…</p></section>
{/if}
