import * as Dialog from '@radix-ui/react-dialog'
import { useMutation, useQuery } from '@tanstack/react-query'
import { Bot, CircleAlert, CircleCheck, LoaderCircle, Plus, Save, TestTube2, Trash2, X } from 'lucide-react'
import { useState, type ReactNode } from 'react'
import { toast } from 'sonner'
import {
  api,
  type AgentSettings,
  type ConnectionTestResult,
  type MCPConnection,
  type MCPConnectionInput,
} from '../api'
import { queryClient, queryKeys } from '../query'

const inputClass =
  'h-10 w-full rounded-xl border border-slate-200 bg-white px-3 text-sm text-slate-800 outline-none transition focus:border-cyan-500/60'

export function AgentSettingsDialog({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const settings = useQuery({
    queryKey: queryKeys.agentSettings,
    queryFn: api.agentSettings,
    enabled: open,
  })
  const connections = useQuery({
    queryKey: queryKeys.agentMCPConnections,
    queryFn: api.agentMCPConnections,
    enabled: open,
  })
  const [draftOverride, setDraft] = useState<AgentSettings | null>(null)
  const draft = draftOverride ?? settings.data ?? null
  const [editing, setEditing] = useState<MCPConnection | null>(null)
  const [newConnectionOpen, setNewConnectionOpen] = useState(false)

  const saveSettings = useMutation({
    mutationFn: () => {
      if (!draft) throw new Error('Agent settings are not loaded')
      return api.saveAgentSettings({
        enabled: draft.enabled,
        provider_base_url: draft.provider_base_url.trim(),
        model: draft.model.trim(),
        api_key_env: draft.api_key_env.trim(),
        request_timeout_ms: draft.request_timeout_ms,
        max_tool_rounds: draft.max_tool_rounds,
        system_instructions: draft.system_instructions.trim(),
      })
    },
    onSuccess: (saved) => {
      queryClient.setQueryData(queryKeys.agentSettings, saved)
      setDraft(saved)
      toast.success('Cycle Agent settings saved')
    },
    onError: (error) => toast.error(error instanceof Error ? error.message : 'Could not save Agent settings'),
  })

  const testProvider = useMutation({
    mutationFn: () => {
      if (!draft) throw new Error('Agent settings are not loaded')
      return api.testAgentProvider({
        provider_base_url: draft.provider_base_url.trim(),
        model: draft.model.trim(),
        api_key_env: draft.api_key_env.trim(),
        request_timeout_ms: draft.request_timeout_ms,
      })
    },
    onError: (error) => toast.error(error instanceof Error ? error.message : 'Could not test provider'),
  })

  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-40 bg-slate-900/35" />
        <Dialog.Content className="fixed left-1/2 top-1/2 z-50 max-h-[88vh] w-[min(94vw,760px)] -translate-x-1/2 -translate-y-1/2 overflow-y-auto rounded-2xl border border-slate-200 bg-slate-50 p-5 shadow-2xl outline-none">
          <Dialog.Title className="flex items-center gap-2 text-lg font-semibold">
            <Bot className="size-5 text-sky-700" /> Built-in Cycle Agent
          </Dialog.Title>
          <Dialog.Description className="mt-1 text-sm leading-6 text-slate-600">
            Provider and MCP secrets stay in process environment variables. CycleDock stores only their environment variable names.
          </Dialog.Description>

          {settings.isPending || !draft ? (
            <div className="py-12 text-center text-sm text-slate-600">Loading Agent settings…</div>
          ) : settings.isError ? (
            <div className="py-12 text-center text-sm text-rose-700">
              {settings.error instanceof Error ? settings.error.message : 'Could not load Agent settings.'}
            </div>
          ) : (
            <>
              <section className="mt-5 rounded-2xl border border-slate-200 bg-white p-4">
                <div className="flex items-center justify-between gap-4">
                  <div>
                    <p className="text-sm font-semibold">Runtime</p>
                    <p className="mt-1 text-xs text-slate-600">
                      Enabled workspaces automatically claim pending Triggers with their dedicated cycle_agent Source.
                    </p>
                  </div>
                  <label className="flex items-center gap-2 text-xs text-slate-600">
                    <input
                      type="checkbox"
                      checked={draft.enabled}
                      onChange={(event) => setDraft({ ...draft, enabled: event.target.checked })}
                      className="size-4 accent-cyan-400"
                    />
                    Enabled
                  </label>
                </div>

                <div className="mt-4 grid gap-4 md:grid-cols-2">
                  <Field label="OpenAI-compatible Base URL">
                    <input
                      value={draft.provider_base_url}
                      onChange={(event) => setDraft({ ...draft, provider_base_url: event.target.value })}
                      placeholder="https://api.example.com/v1"
                      className={inputClass}
                    />
                  </Field>
                  <Field label="Model">
                    <input
                      value={draft.model}
                      onChange={(event) => setDraft({ ...draft, model: event.target.value })}
                      placeholder="gpt-compatible-model"
                      className={inputClass}
                    />
                  </Field>
                  <Field label="API key environment variable">
                    <input
                      value={draft.api_key_env}
                      onChange={(event) => setDraft({ ...draft, api_key_env: event.target.value })}
                      placeholder="CYCLEDOCK_MODEL_API_KEY"
                      className={inputClass}
                    />
                  </Field>
                  <Field label="Request timeout (ms)">
                    <input
                      type="number"
                      min={1000}
                      max={600000}
                      value={draft.request_timeout_ms}
                      onChange={(event) => setDraft({ ...draft, request_timeout_ms: Number(event.target.value) })}
                      className={inputClass}
                    />
                  </Field>
                  <Field label="Max tool rounds">
                    <input
                      type="number"
                      min={1}
                      max={32}
                      value={draft.max_tool_rounds}
                      onChange={(event) => setDraft({ ...draft, max_tool_rounds: Number(event.target.value) })}
                      className={inputClass}
                    />
                  </Field>
                </div>

                <Field label="Workspace Agent instructions" className="mt-4">
                  <textarea
                    rows={4}
                    value={draft.system_instructions}
                    onChange={(event) => setDraft({ ...draft, system_instructions: event.target.value })}
                    placeholder="Durable workspace-level instructions for every built-in Agent Run."
                    className={inputClass + ' h-auto resize-y py-2'}
                  />
                </Field>

                <div className="mt-4 flex flex-wrap gap-2">
                  <button
                    type="button"
                    disabled={saveSettings.isPending || (draft.enabled && (!draft.provider_base_url.trim() || !draft.model.trim()))}
                    onClick={() => saveSettings.mutate()}
                    className="inline-flex h-9 items-center gap-2 rounded-xl bg-cyan-400 px-4 text-xs font-semibold text-slate-950 disabled:opacity-40"
                  >
                    <Save className="size-3.5" /> Save runtime
                  </button>
                  <button
                    type="button"
                    disabled={testProvider.isPending || !draft.provider_base_url.trim() || !draft.model.trim()}
                    onClick={() => testProvider.mutate()}
                    className="inline-flex h-9 items-center gap-2 rounded-xl border border-slate-300 px-4 text-xs font-semibold text-slate-700 transition hover:border-slate-400 disabled:opacity-40"
                  >
                    {testProvider.isPending ? <LoaderCircle className="size-3.5 animate-spin" /> : <TestTube2 className="size-3.5" />}
                    Test provider
                  </button>
                </div>
                {testProvider.data ? <ConnectionTestStatus result={testProvider.data} /> : null}
              </section>

              <section className="mt-4 rounded-2xl border border-slate-200 bg-white p-4">
                <div className="flex items-center justify-between gap-4">
                  <div>
                    <p className="text-sm font-semibold">MCP connections</p>
                    <p className="mt-1 text-xs text-slate-600">
                      Tools are discovered at Run start. Bearer tokens are resolved from environment only.
                    </p>
                  </div>
                  <button
                    type="button"
                    onClick={() => setNewConnectionOpen(true)}
                    className="inline-flex h-8 items-center gap-1.5 rounded-lg border border-slate-300 px-2.5 text-xs text-slate-700 hover:border-slate-400"
                  >
                    <Plus className="size-3.5" /> Add
                  </button>
                </div>

                {connections.isPending ? (
                  <p className="mt-5 text-xs text-slate-600">Loading connections…</p>
                ) : connections.isError ? (
                  <p className="mt-5 text-xs text-rose-700">
                    {connections.error instanceof Error ? connections.error.message : 'Could not load connections.'}
                  </p>
                ) : (connections.data ?? []).length === 0 ? (
                  <p className="mt-5 rounded-xl border border-dashed border-slate-200 p-4 text-xs text-slate-600">
                    No outbound MCP connections configured.
                  </p>
                ) : (
                  <div className="mt-4 space-y-2">
                    {(connections.data ?? []).map((connection) => (
                      <button
                        type="button"
                        key={connection.id}
                        onClick={() => setEditing(connection)}
                        className="flex w-full items-center justify-between gap-4 rounded-xl border border-slate-200 px-3 py-3 text-left transition hover:border-slate-300"
                      >
                        <div className="min-w-0">
                          <p className="truncate text-sm font-medium text-slate-800">{connection.name}</p>
                          <p className="mt-1 truncate text-[11px] text-slate-600">{connection.url}</p>
                        </div>
                        <span
                          className={'shrink-0 text-[10px] uppercase tracking-wider ' + (connection.enabled ? 'text-emerald-400' : 'text-slate-600')}
                        >
                          {connection.enabled ? 'enabled' : 'disabled'}
                        </span>
                      </button>
                    ))}
                  </div>
                )}
              </section>
            </>
          )}

          <Dialog.Close aria-label="Close" className="absolute right-4 top-4 grid size-8 place-items-center rounded-lg text-slate-600 hover:bg-white hover:text-slate-700">
            <X className="size-4" />
          </Dialog.Close>

          {newConnectionOpen ? (
            <MCPConnectionEditor
              onClose={() => setNewConnectionOpen(false)}
              onSaved={async () => {
                await queryClient.invalidateQueries({ queryKey: queryKeys.agentMCPConnections })
                setNewConnectionOpen(false)
              }}
            />
          ) : null}
          {editing ? (
            <MCPConnectionEditor
              connection={editing}
              onClose={() => setEditing(null)}
              onSaved={async () => {
                await queryClient.invalidateQueries({ queryKey: queryKeys.agentMCPConnections })
                setEditing(null)
              }}
            />
          ) : null}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  )
}

function MCPConnectionEditor({
  connection,
  onClose,
  onSaved,
}: {
  connection?: MCPConnection
  onClose: () => void
  onSaved: () => Promise<void>
}) {
  const [draft, setDraft] = useState<MCPConnectionInput>({
    name: connection?.name ?? '',
    url: connection?.url ?? '',
    bearer_token_env: connection?.bearer_token_env ?? '',
    enabled: connection?.enabled ?? true,
    timeout_ms: connection?.timeout_ms ?? 30000,
  })
  const save = useMutation({
    mutationFn: () =>
      connection ? api.updateAgentMCPConnection(connection.id, draft) : api.createAgentMCPConnection(draft),
    onSuccess: async () => {
      await onSaved()
      toast.success(connection ? 'MCP connection updated' : 'MCP connection added')
    },
    onError: (error) => toast.error(error instanceof Error ? error.message : 'Could not save MCP connection'),
  })
  const remove = useMutation({
    mutationFn: () => api.deleteAgentMCPConnection(connection!.id),
    onSuccess: async () => {
      await onSaved()
      toast.success('MCP connection removed')
    },
    onError: (error) => toast.error(error instanceof Error ? error.message : 'Could not remove MCP connection'),
  })
  const testConnection = useMutation({
    mutationFn: () =>
      api.testAgentMCP({
        name: draft.name.trim(),
        url: draft.url.trim(),
        bearer_token_env: draft.bearer_token_env.trim(),
        timeout_ms: draft.timeout_ms,
      }),
    onError: (error) => toast.error(error instanceof Error ? error.message : 'Could not test MCP connection'),
  })

  return (
    <div className="absolute inset-0 z-10 flex items-center justify-center rounded-2xl bg-white/95 p-5">
      <div className="w-full max-w-lg rounded-2xl border border-slate-200 bg-slate-50 p-4 shadow-2xl">
        <div className="flex items-center justify-between">
          <p className="text-sm font-semibold">{connection ? 'Edit MCP connection' : 'New MCP connection'}</p>
          <button type="button" onClick={onClose} aria-label="Close MCP connection editor" className="grid size-8 place-items-center rounded-lg text-slate-600 hover:bg-white">
            <X className="size-4" />
          </button>
        </div>
        <div className="mt-4 grid gap-4">
          <Field label="Name">
            <input value={draft.name} onChange={(event) => setDraft({ ...draft, name: event.target.value })} className={inputClass} />
          </Field>
          <Field label="Remote MCP URL">
            <input value={draft.url} onChange={(event) => setDraft({ ...draft, url: event.target.value })} placeholder="https://mcp.example.com/mcp" className={inputClass} />
          </Field>
          <Field label="Bearer token environment variable">
            <input value={draft.bearer_token_env} onChange={(event) => setDraft({ ...draft, bearer_token_env: event.target.value })} placeholder="OPTIONAL_MCP_TOKEN" className={inputClass} />
          </Field>
          <Field label="Timeout (ms)">
            <input type="number" min={1000} max={300000} value={draft.timeout_ms} onChange={(event) => setDraft({ ...draft, timeout_ms: Number(event.target.value) })} className={inputClass} />
          </Field>
          <label className="flex items-center gap-2 text-xs text-slate-600">
            <input type="checkbox" checked={draft.enabled} onChange={(event) => setDraft({ ...draft, enabled: event.target.checked })} className="size-4 accent-cyan-400" />
            Enabled
          </label>
        </div>
        <div className="mt-5 flex flex-wrap gap-2">
          <button
            type="button"
            onClick={() => save.mutate()}
            disabled={save.isPending || !draft.name.trim() || !draft.url.trim()}
            className="flex h-9 flex-1 items-center justify-center gap-2 rounded-xl bg-slate-100 text-xs font-semibold text-slate-950 disabled:opacity-40"
          >
            <Save className="size-3.5" /> Save
          </button>
          <button
            type="button"
            onClick={() => testConnection.mutate()}
            disabled={testConnection.isPending || !draft.name.trim() || !draft.url.trim()}
            className="inline-flex h-9 items-center gap-2 rounded-xl border border-slate-300 px-3 text-xs font-semibold text-slate-700 transition hover:border-slate-400 disabled:opacity-40"
          >
            {testConnection.isPending ? <LoaderCircle className="size-3.5 animate-spin" /> : <TestTube2 className="size-3.5" />}
            Test
          </button>
          {connection ? (
            <button
              type="button"
              onClick={() => remove.mutate()}
              disabled={remove.isPending}
              className="grid size-9 place-items-center rounded-xl border border-rose-200 text-rose-700 hover:bg-rose-50"
              aria-label="Delete MCP connection"
            >
              <Trash2 className="size-3.5" />
            </button>
          ) : null}
        </div>
        {testConnection.data ? <ConnectionTestStatus result={testConnection.data} /> : null}
      </div>
    </div>
  )
}

function ConnectionTestStatus({ result }: { result: ConnectionTestResult }) {
  return (
    <div
      className={
        'mt-3 flex items-start gap-2 rounded-xl border px-3 py-2.5 text-xs leading-5 ' +
        (result.ok
          ? 'border-emerald-200 bg-emerald-50 text-emerald-800'
          : 'border-amber-200 bg-amber-50 text-amber-800')
      }
      role="status"
      aria-live="polite"
    >
      {result.ok ? <CircleCheck className="mt-0.5 size-3.5 shrink-0" /> : <CircleAlert className="mt-0.5 size-3.5 shrink-0" />}
      <div>
        <p>{result.message}</p>
        <p className="mt-0.5 text-[10px] opacity-70">
          {result.code}
          {typeof result.tool_count === 'number' ? ` · ${result.tool_count} tools` : ''}
          {` · ${result.duration_ms} ms`}
        </p>
      </div>
    </div>
  )
}

function Field({
  label,
  className = '',
  children,
}: {
  label: string
  className?: string
  children: ReactNode
}) {
  return (
    <label className={'block ' + className}>
      <span className="mb-2 block text-xs font-medium text-slate-600">{label}</span>
      {children}
    </label>
  )
}
