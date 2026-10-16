export type MachineBackend = 'lxd' | 'lxc' | 'kvm'
export interface MachineCapability { id: MachineBackend; title: string; available: boolean; reason?: string; notice?: string; context: string; actions: string[] }
export interface Machine { backend: MachineBackend; name: string; kind: string; state: string; manageable: boolean; architecture?: string; location?: string; ips: string[]; cpus?: number; memoryBytes?: number; uuid?: string; autostart?: boolean; persistent?: boolean }
export interface MachineInventory { backends: MachineCapability[]; machines: Machine[] }
export const backendTitles: Record<MachineBackend, string> = { lxd: 'LXD', lxc: 'Classic LXC', kvm: 'KVM / libvirt' }
export const machineActions: Record<string, string> = { start: 'Start', stop: 'Stop', restart: 'Restart', delete: 'Delete instance', shutdown: 'Shut down', reboot: 'Reboot', undefine: 'Undefine domain', autostart: 'Set autostart', launch: 'Launch instance', define: 'Define domain' }
