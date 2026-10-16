<script lang="ts">
  import { onMount } from 'svelte'
  import { invokeTool, type Job } from './api'

  let { allowWrites }: { allowWrites: boolean } = $props()
  let open = $state(false)
  let jobs = $state<Job[]>([])
  let selected = $state<Job | null>(null)
  let error = $state('')
  let cancelling = $state(false)
  const activeCount = $derived(jobs.filter(j => j.state === 'running').length)
  let refresh: () => Promise<void> = async () => {}

  onMount(() => {
    let stopped = false
    let polling = false
    let timer: ReturnType<typeof setTimeout>
    const controller = new AbortController()
    async function poll() {
      if (stopped || polling) return
      polling = true
      clearTimeout(timer)
      try {
        jobs = await invokeTool<Job[]>('jobs_list', {}, controller.signal)
        if (selected && open) selected = await invokeTool<Job>('jobs_get', { id: selected.id }, controller.signal)
        error = ''
      } catch (e) { if (!stopped) error = e instanceof Error ? e.message : 'Could not read jobs' }
      finally { polling = false; if (!stopped) timer = setTimeout(poll, open ? 2000 : 5000) }
    }
    refresh = poll
    void poll()
    return () => { stopped = true; clearTimeout(timer); controller.abort() }
  })

  async function select(job: Job) {
    selected = job
    await refresh()
  }

  async function cancel() {
    if (!selected) return
    cancelling = true
    try { await invokeTool('jobs_cancel', { id: selected.id }); await refresh() }
    catch (e) { error = e instanceof Error ? e.message : 'Cancellation failed' }
    finally { cancelling = false }
  }
</script>

<button class="activity-button" aria-expanded={open} aria-controls="job-drawer" onclick={() => { open = !open; if (open) void refresh() }}>Activity{#if activeCount}<span class="activity-count">{activeCount}</span>{/if}</button>
{#if open}
  <section id="job-drawer" class="job-drawer panel" aria-label="Management jobs">
    <div class="job-header"><h2>Recent activity</h2><button class="small-button" onclick={() => { open = false }}>Close</button></div>
    {#if error}<p class="form-error" role="alert">{error}</p>{/if}
    {#if jobs.length === 0}<div class="job-empty"><h3>No management jobs yet</h3><p>Changes you make to the server will appear here.</p></div>{/if}
    <div class="job-list">{#each jobs as job}<button class:selected={selected?.id === job.id} class="job-row" onclick={() => select(job)}><span><strong>{job.tool.replaceAll('_', ' ')}</strong><time datetime={job.createdAt}>{new Date(job.createdAt).toLocaleTimeString()}</time></span><span class="job-state" class:failed={job.state === 'failed'}>{job.state}</span></button>{/each}</div>
    {#if selected}
      <div class="job-detail">
        <div class="job-detail-heading"><strong>{selected.tool.replaceAll('_', ' ')}</strong>{#if selected.state === 'running' && allowWrites}<button class="small-button" disabled={cancelling} onclick={cancel}>{cancelling ? 'Cancelling…' : 'Cancel job'}</button>{/if}</div>
        {#if selected.error}<p class="form-error">{selected.error}</p>{/if}
        {#if selected.truncated}<p class="muted">Showing the most recent 128 KiB of output.</p>{/if}
        <pre>{selected.output || (selected.state === 'running' ? 'Waiting for output…' : 'This operation produced no output.')}</pre>
      </div>
    {/if}
  </section>
{/if}
