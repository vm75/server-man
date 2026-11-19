<script lang="ts">
  let { name, size = 20 }: { name: string; size?: number } = $props()
  const paths: Record<string, string> = {
    cpu: 'M7 7h10v10H7z M10 10h4v4h-4z M9 3v4 M15 3v4 M9 17v4 M15 17v4 M3 9h4 M3 15h4 M17 9h4 M17 15h4',
    memory: 'M3 5h18v13H3z M7 9h3v5H7z M14 9h3v5h-3z M6 18v3 M10 18v3 M14 18v3 M18 18v3',
    network: 'M9 3h6v6H9z M2 16h6v5H2z M16 16h6v5h-6z M12 9v4 M5 16v-3h14v3',
    drive: 'M5 4h14l3 12H2z M2 16v5h20v-5 M6 18.5h.01 M17 18.5h2',
    server: 'M4 3h16v7H4z M4 14h16v7H4z M8 6.5h.01 M8 17.5h.01 M12 6.5h5 M12 17.5h5',
    overview: 'M3 3h7v7H3z M14 3h7v7h-7z M3 14h7v7H3z M14 14h7v7h-7z',
    applications: 'M12 2L2 7l10 5 10-5-10-5z M2 17l10 5 10-5 M2 12l10 5 10-5',
    configuration: 'M4 21v-7 M4 10V3 M12 21v-9 M12 8V3 M20 21v-5 M20 12V3 M1 14h6 M9 8h6 M17 16h6',
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
    sun: 'M12 8a4 4 0 1 0 0 8 4 4 0 0 0 0-8z M12 2v2 M12 20v2 M4.93 4.93l1.41 1.41 M17.66 17.66l1.41 1.41 M2 12h2 M20 12h2 M6.34 17.66l-1.41 1.41 M19.07 4.93l-1.41 1.41',
    moon: 'M12 3a9 9 0 1 0 9 9c0-.46-.04-.92-.1-1.36a5.5 5.5 0 0 1-7.54-7.54A8.99 8.99 0 0 0 12 3z',
  }
</script>

<svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
  <path d={paths[name] ?? paths.server} />
</svg>
