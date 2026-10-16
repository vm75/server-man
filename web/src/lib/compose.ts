export interface ComposeEngine { name: string; root: string; available: boolean; reason?: string; notice?: string }
export interface Project { engine: string; name: string; directory: string; file: string; envFiles: string[]; disabled: boolean; state: string; containers: number; running: number }
export interface ComposeInventory { engines: ComposeEngine[]; projects: Project[]; warnings: string[] }
export interface Container { id: string; name: string; service: string; image: string; state: string; status: string; health: string; ports: string; podID?: string; podName?: string; infra: boolean }
export interface Pod { id: string; name: string; state: string; containers: number }
export interface ProjectDetails { project: Project; containers: Container[]; pods: Pod[]; warnings: string[] }
export interface ProjectLogs { text: string; truncated: boolean }
export type ComposeAction = 'up' | 'start' | 'stop' | 'restart' | 'pull' | 'down'
export const actionLabels: Record<ComposeAction, string> = { up: 'Apply & start', start: 'Start', stop: 'Stop', restart: 'Restart', pull: 'Pull images', down: 'Remove containers' }
export const destructive = new Set<ComposeAction>(['up', 'stop', 'restart', 'down'])
export const stateLabels: Record<string, string> = { unknown: 'Not checked', not_detected: 'Not detected', not_created: 'Not created', running: 'Running', stopped: 'Stopped', partial: 'Partly running', paused: 'Paused', degraded: 'Needs attention' }
export function key(project: Project): string { return `${project.engine}/${project.name}` }
