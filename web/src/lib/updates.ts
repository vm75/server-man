export interface PackageUpdate { name: string; architecture?: string; installed?: string; candidate?: string; kind: 'upgrade' | 'install' | 'held' }
export interface UpdateStatus { manager: { id: string; title: string; available: boolean; reason?: string; canRefresh: boolean; refreshReason?: string }; distribution: string; packages: PackageUpdate[]; valid: boolean; notice: string; cacheSource: string; reboot: { required: boolean | null; reason: string }; readAt: string }
export const updateKinds: Record<string, string> = { upgrade: 'Upgrade', install: 'Dependency', held: 'Deferred' }
