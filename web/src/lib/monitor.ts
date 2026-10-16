export interface Host { hostname: string; os: string; kernel: string; architecture: string; cpuModel: string }
export interface CPU { usage: number | null; cores: { name: string; usage: number | null }[]; frequencyMHz: number | null }
export interface Memory { total: number; available: number; used: number; cached: number; buffers: number; swapTotal: number; swapUsed: number }
export interface Disk { device: string; mount: string; filesystem: string; total: number; used: number; available: number }
export interface DiskIO { name: string; readBytesPerSecond: number | null; writeBytesPerSecond: number | null }
export interface Network { name: string; rxBytes: number; txBytes: number; rxBytesPerSecond: number | null; txBytesPerSecond: number | null }
export interface Point { at: string; cpu: number | null; memoryPercent: number; rx: number | null; tx: number | null }
export interface Snapshot {
  valid: boolean
  at: string
  intervalSeconds: number
  host: Host
  cpu: CPU
  memory: Memory
  uptimeSeconds: number | null
  load: [number, number, number] | null
  disks: Disk[]
  diskIO: DiskIO[]
  networks: Network[]
  temperatures: { name: string; celsius: number }[]
  processCount: number
  processesLimited: boolean
  history: Point[]
  warnings: string[]
  lastError?: string
}
export interface Process { pid: number; name: string; state: string; cpu: number | null; memory: number; threads: number; nice: number }
export interface ProcessList { at: string; processes: Process[]; matched: number; limited: boolean }

export function bytes(value: number | null | undefined): string {
  if (value == null || !Number.isFinite(value)) return '—'
  if (value < 1024) return `${Math.round(value)} B`
  const units = ['KiB', 'MiB', 'GiB', 'TiB', 'PiB']
  let scaled = value / 1024, index = 0
  while (scaled >= 1024 && index < units.length - 1) { scaled /= 1024; index++ }
  return `${scaled.toFixed(scaled >= 100 ? 0 : 1)} ${units[index]}`
}
export function rate(value: number | null | undefined): string { return value == null ? '—' : `${bytes(value)}/s` }
export function percent(value: number | null | undefined): string { return value == null || !Number.isFinite(value) ? '—' : `${value.toFixed(1)}%` }
export function uptime(value: number | null | undefined): string {
  if (value == null) return '—'
  const days = Math.floor(value / 86400), hours = Math.floor(value % 86400 / 3600), minutes = Math.floor(value % 3600 / 60)
  return days ? `${days}d ${hours}h` : hours ? `${hours}h ${minutes}m` : `${minutes}m`
}
