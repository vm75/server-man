export interface Service { name: string; description: string; load: string; active: string; sub: string; enabled: string; preset?: string }
export interface ServiceInventory { scope: 'system' | 'user'; available: boolean; journalAvailable: boolean; reason?: string; services: Service[]; warnings: string[] }
export interface ServiceDetails extends Service { mainPID?: number; result?: string; execMainCode?: string; execMainStatus?: string; activeSince?: string; inactiveSince?: string; fragmentPath?: string; canStart: boolean; canStop: boolean; canReload: boolean }
export interface ServiceLogs { text: string; truncated: boolean }
export const serviceActions: Record<string, string> = { start: 'Start', stop: 'Stop', restart: 'Restart', reload: 'Reload', enable: 'Enable at startup', disable: 'Disable at startup' }
