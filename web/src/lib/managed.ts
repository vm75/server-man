export interface Mount { storage: string; target: string; readOnly: boolean }
export interface ContainerDefinition { image: string; command?: string[]; ports?: string[]; environment?: Record<string, string>; envFiles?: string[]; mounts?: Mount[]; memoryMiB?: number; cpus?: number }
export interface VMDefinition { uuid?: string; mac?: string; image?: string; cpus: number; memoryMiB: number; disks?: string[]; network: string; pool?: string; project?: string; rootSizeGiB?: number; architecture?: string; machine?: string; firmware?: string }
export interface ServiceDefinition { name?: string; description?: string; kind: 'container' | 'vm'; engine?: string; backend?: string; enabled: boolean; dependsOn?: string[]; containers?: Record<string, ContainerDefinition>; vm?: VMDefinition }
export interface DiskSource { path: string; format: string; sha256: string }
export interface StorageDefinition { source?: DiskSource; path: string; kind?: string; external?: boolean; format?: string; mount?: string }
export interface ShareDefinition { protocol: string; storage: string; path?: string; enabled: boolean; readOnly: boolean; guest?: boolean; browseable?: boolean; users?: string[]; clients?: string[]; rootSquash?: boolean; security?: string }
export interface BindingDefinition { kind: string; name: string }
export interface ManagedConfig { server?: Record<string, string | number | boolean>; version: number; services: Record<string, ServiceDefinition> | null; storage: Record<string, StorageDefinition> | null; shares: Record<string, ShareDefinition> | null; bindings: Record<string, BindingDefinition> | null; secrets: Record<string, string> | null }
export interface ModelSnapshot { revision: string; config: ManagedConfig }
export type ResourceKind = 'service' | 'storage' | 'share' | 'binding' | 'secret'
export interface ImpactPlan { restartRequired?: boolean; baseRevision: string; desiredRevision: string; changes: { resource: string; backend: string; operation: string }[]; files: { issues: { resource: string; message: string; remedy: string }[] } }

export interface ConfigurationIssue { severity: string; field: string; resource: string; code: string; message: string; remedy: string }
export interface ConfigurationHealth { checkedAt: string; runtimeCheckedAt?: string; issues: ConfigurationIssue[] }
export interface ConfigurationReadiness { configured: boolean; valid: boolean; error?: string; health?: ConfigurationHealth }
