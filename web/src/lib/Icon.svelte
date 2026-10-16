<script lang="ts">
  let { name, size = 20 }: { name: string; size?: number } = $props()
  const paths: Record<string, string> = {
    server: 'M4 3h16v7H4z M4 14h16v7H4z M8 6.5h.01 M8 17.5h.01 M12 6.5h5 M12 17.5h5',
    overview: 'M3 3h7v7H3z M14 3h7v7h-7z M3 14h7v7H3z M14 14h7v7h-7z',
    containers: 'M12 3 3 8v9l9 5 9-5V8z M3 8l9 5 9-5 M12 13v9 M7 5.8l9 5',
    machines: 'M3 4h18v13H3z M8 21h8 M12 17v4 M7 8h4 M7 12h8',
    services: 'M4 4h16v16H4z M8 8l3 4-3 4 M14 16h3',
    shares: 'M3 6h7l2 2h9v12H3z M8 13h8 M12 10v6',
    updates: 'M20 7v5h-5 M4 17v-5h5 M5.5 7A7 7 0 0 1 19 9 M5 15a7 7 0 0 0 13.5 2',
    agent: 'M8 7V5a4 4 0 0 1 8 0v2 M5 7h14v14H5z M9 12h.01 M15 12h.01 M9 17h6',
    menu: 'M4 6h16 M4 12h16 M4 18h16',
    arrow: 'M5 12h14 M13 6l6 6-6 6',
    check: 'M5 12l4 4L19 6',
    close: 'M6 6l12 12 M6 18 18 6',
  }
</script>

<svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
  <path d={paths[name] ?? paths.server} />
</svg>
