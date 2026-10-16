export type ShareProtocol = 'samba' | 'nfs'
export interface ShareDefinition { name: string; path: string; readOnly: boolean; guest?: boolean; browseable?: boolean; users?: string[]; clients?: string[]; rootSquash?: boolean; security?: string }
export interface Share extends ShareDefinition { protocol: ShareProtocol; managed: boolean; configured: boolean; runtime: string; connections?: number }
export interface ShareBackend { protocol: ShareProtocol; available: boolean; canManage: boolean; reason?: string; managedFile: string; revision: string; setupRequired: boolean; notice?: string }
export interface ShareInventory { backends: ShareBackend[]; shares: Share[]; warnings: string[] }
export const runtimeLabels: Record<string, string> = { unknown: 'Runtime unknown', connected: 'Clients connected', not_observed: 'No connected clients observed', exported: 'Registered in export table', not_exported: 'Not registered', mismatch: 'Export options differ' }
