export interface Status {
  hostname: string
  startedAt: string
  version: string
  allowWrites: boolean
}

export interface Job {
  id: string
  tool: string
  state: 'running' | 'succeeded' | 'failed' | 'cancelled'
  createdAt: string
  finishedAt?: string
  output: string
  truncated: boolean
  error?: string
  result?: unknown
}

export interface Module { id: string; title: string; available: boolean; reason?: string }
export interface Tool { name: string; module: string; title: string; description: string; readOnly: boolean; destructive: boolean; inputSchema: Record<string, unknown> }
export interface Discovery { tools: Tool[]; modules: Module[] }

export class ApiError extends Error {
  constructor(message: string, public status: number, public code?: string) { super(message) }
}

export async function request<T>(path: string, options: RequestInit = {}): Promise<T> {
  const response = await fetch(path, {
    ...options,
    headers: { 'Content-Type': 'application/json', ...options.headers },
    signal: options.signal ? AbortSignal.any([options.signal, AbortSignal.timeout(15_000)]) : AbortSignal.timeout(15_000),
  })
  if (!response.ok) {
    const body = await response.json().catch(() => ({ error: response.statusText }))
    if (response.status === 401) window.dispatchEvent(new Event('server-man:unauthorized'))
    const message = typeof body.error === 'string' ? body.error : body.error?.message
    throw new ApiError(message || `Request failed (${response.status})`, response.status, body.error?.code)
  }
  return response.json() as Promise<T>
}

export function invokeTool<T>(name: string, arguments_: Record<string, unknown> = {}, signal?: AbortSignal): Promise<T> {
  return request<T>(`/api/tools/${encodeURIComponent(name)}`, { method: 'POST', body: JSON.stringify(arguments_), signal })
}

export async function startJob(name: string, arguments_: Record<string, unknown>): Promise<Job> {
  const result = await invokeTool<{ job: Job }>(name, arguments_)
  window.dispatchEvent(new CustomEvent<Job>('server-man:job-started', { detail: result.job }))
  return result.job
}
