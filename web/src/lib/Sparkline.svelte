<script lang="ts">
  let { values, times, windowSeconds, secondary = [], ceiling, label, color = '#7be5b3', secondaryColor = '#77b7ee' }: {
    values: (number | null)[]; times: number[]; windowSeconds: number; secondary?: (number | null)[]; ceiling?: number; label: string; color?: string; secondaryColor?: string
  } = $props()
  const maximum = $derived(ceiling ?? Math.max(1, ...values.filter((n): n is number => n != null), ...secondary.filter((n): n is number => n != null)))

  function paths(data: (number | null)[]) {
    const segments: { line: string; area: string }[] = []
    let line = '', firstX = 0, lastX = 0
    function finish() { if (line) segments.push({ line, area: `${line} L ${lastX} 100 L ${firstX} 100 Z` }); line = '' }
    data.forEach((value, index) => {
      if (value == null || !Number.isFinite(value)) { finish(); return }
      if (index > 0 && times[index] - times[index - 1] > windowSeconds / 60 * 1500) finish()
      const x = Math.min(600, Math.max(0, 600 - ((times.at(-1) ?? 0) - times[index]) / (windowSeconds * 1000) * 600))
      const y = 98 - Math.min(1, Math.max(0, value / maximum)) * 94
      if (!line) firstX = x
      line += `${line ? ' L' : 'M'} ${x.toFixed(2)} ${y.toFixed(2)}`
      lastX = x
    })
    finish()
    return segments
  }
</script>

<svg class="sparkline" viewBox="0 0 600 100" preserveAspectRatio="none" role="img" aria-label={label}>
  {#each [25, 50, 75, 99] as y}<line x1="0" x2="600" y1={y} y2={y} stroke="#3b4858" stroke-dasharray="2 7" stroke-width=".6" />{/each}
  {#each paths(values) as path}<path d={path.area} fill={color} opacity=".07" /><path d={path.line} fill="none" stroke={color} stroke-width="1.8" vector-effect="non-scaling-stroke" />{/each}
  {#each paths(secondary) as path}<path d={path.line} fill="none" stroke={secondaryColor} stroke-width="1.8" vector-effect="non-scaling-stroke" />{/each}
</svg>
