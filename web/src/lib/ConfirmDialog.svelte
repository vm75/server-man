<script lang="ts">
  let { open, title, description, label = 'Confirm', busy = false, onConfirm, onClose }: { open: boolean; title: string; description: string; label?: string; busy?: boolean; onConfirm: () => void; onClose: () => void } = $props()
  let dialog = $state<HTMLDialogElement>()
  const headingID = $props.id()
  $effect(() => { if (!dialog) return; if (open && !dialog.open) dialog.showModal(); else if (!open && dialog.open) dialog.close() })
</script>

<dialog bind:this={dialog} class="confirm-dialog" aria-labelledby={headingID} oncancel={event => { if (busy) event.preventDefault(); else onClose() }}>
  <div class="eyebrow">CONFIRM SERVER CHANGE</div><h2 id={headingID}>{title}</h2><p>{description}</p><div class="dialog-actions"><button onclick={onClose} disabled={busy}>Cancel</button><button class="danger-button" onclick={onConfirm} disabled={busy}>{busy ? 'Starting…' : label}</button></div>
</dialog>
