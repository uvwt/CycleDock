import * as Dialog from '@radix-ui/react-dialog'
import { useMutation, useQuery } from '@tanstack/react-query'
import { Bot, CircleAlert, CircleCheck, ExternalLink, LoaderCircle, LogOut, Plus, Save, TestTube2, Trash2, X } from 'lucide-react'
import { useState, type ReactNode } from 'react'
import { toast } from 'sonner'
import {
  api,
  type AgentProviderType,
  type AgentSettings,
  type ConnectionTestResult,
  type GrokDeviceFlow,
  type MCPConnection,
  type MCPConnectionInput,
} from '../api'
import { queryClient, queryKeys } from '../query'

const inputClass =
  'h-10 w-full rounded-xl border border-slate-200 bg-white px-3 text-sm text-slate-800 outline-none transition focus:border-cyan-500/60'

const providerOptions: Array<{ id: AgentProviderType; title: string; description: string }> = [
  { id: 'chatgpt', title: 'ChatGPT Plan', description: 'Use the signed-in account’s eligible ChatGPT plan usage.' },
  { id: 'grok', title: 'Grok Subscription', description: 'Use SuperGrok / Grok subscription access through xAI OAuth.' },
  { id: 'openai_compatible', title: 'API Provider', description: 'Use an OpenAI-compatible endpoint and deployment secret.' },
]

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
  const modelAuth = useQuery({
    queryKey: queryKeys.modelAuth,
    queryFn: api.modelAuthStatus,
    enabled: open,
  })

  const [draftOverride, setDraft] = useState<AgentSettings | null>(null)
  const draft = draftOverride ?? settings.data ?? null
  const [editing, setEditing] = useState<MCPConnection | null>(null)
  const [newConnectionOpen, setNewConnectionOpen] = useState(false)
  const [grokFlow, setGrokFlow] = useState<GrokDeviceFlow | null>(null)

  const subscriptionProvider =
    draft?.provider_type === 'chatgpt' || draft?.provider_type === 'grok' ? draft.provider_type : null
  const subscriptionStatus = subscriptionProvider
    ? modelAuth.data?.find((item) => item.provider === subscriptionProvider)
    : undefined
  const models = useQuery({
    queryKey: queryKeys.agentModels(subscriptionProvider ?? 'none'),
    queryFn: () => {
      if (!subscriptionProvider) throw new Error('Subscription provider is not selected')
      return api.agentModels(subscriptionProvider)
    },
    enabled:
      open &&
      Boolean(subscriptionProvider && subscriptionStatus?.available && subscriptionStatus.connected),
  })

  const saveSettings = useMutation({
    mutationFn: () => {
      if (!draft) throw new Error('Agent settings are not loaded')
      return api.saveAgentSettings({
        enabled: draft.enabled,
        provider_type: draft.provider_type,
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
        provider_type: draft.provider_type,
        provider_base_url: draft.provider_base_url.trim(),
        model: draft.model.trim(),
        api_key_env: draft.api_key_env.trim(),
        request_timeout_ms: draft.request_timeout_ms,
      })
    },
    onError: (error) => toast.error(error instanceof Error ? error.message : 'Could not test provider'),
  })

  const connectChatGPT = useMutation({
    mutationFn: api.startChatGPTModelAuth,
    onSuccess: ({ authorization_url }) => window.location.assign(authorization_url),
    onError: (error) => toast.error(error instanceof Error ? error.message : 'Could not start ChatGPT sign-in'),
  })

  const connectGrok = useMutation({
    mutationFn: api.startGrokModelAuth,
    onSuccess: (flow) => {
      setGrokFlow(flow)
      window.open(flow.verification_url, '_blank', 'noopener,noreferrer')
    },
    onError: (error) => toast.error(error instanceof Error ? error.message : 'Could not start Grok sign-in'),
  })

  const pollGrok = useMutation({
    mutationFn: (flowID: string) => api.pollGrokModelAuth(flowID),
    onSuccess: async (result) => {
      if (result.status !== 'connected') {
        toast.message('Grok is still waiting for authorization')
        return
      }
      setGrokFlow(null)
      await queryClient.invalidateQueries({ queryKey: queryKeys.modelAuth })
      await queryClient.invalidateQueries({ queryKey: queryKeys.agentModels('grok') })
      toast.success('Grok account connected')
    },
    onError: (error) => toast.error(error instanceof Error ? error.message : 'Could not finish Grok sign-in'),
  })

  const disconnect = useMutation({
    mutationFn: (provider: 'chatgpt' | 'grok') => api.disconnectModelAuth(provider),
    onSuccess: async (_, provider) => {
      if (draft?.provider_type === provider) {
        setDraft({ ...draft, enabled: false, model: '' })
      }
      await queryClient.invalidateQueries({ queryKey: queryKeys.modelAuth })
      queryClient.removeQueries({ queryKey: queryKeys.agentModels(provider) })
      toast.success('Model account disconnected')
    },
    onError: (error) => toast.error(error instanceof Error ? error.message : 'Could not disconnect model account'),
  })

  const providerReady = draft
    ? draft.provider_type === 'openai_compatible'
      ? Boolean(draft.provider_base_url.trim() && draft.model.trim())
      : Boolean(subscriptionStatus?.available && subscriptionStatus.connected && draft.model.trim())
    : false

  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-40 bg-slate-900/35" />
        <Dialog.Content className="fixed left-1/2 top-1/2 z-50 max-h-[88vh] w-[min(94vw,760px)] -translate-x-1/2 -translate-y-1/2 overflow-y-auto rounded-2xl border border-slate-200 bg-slate-50 p-5 shadow-2xl outline-none">
          <Dialog.Title className="flex items-center gap-2 text-lg font-semibold">
            <Bot className="size-5 text-sky-700" /> Built-in Cycle Agent
          </Dialog.Title>
          <Dialog.Description className="mt-1 text-sm leading-6 text-slate-600">
            Connect an eligible ChatGPT or Grok subscription, or keep using an OpenAI-compatible API endpoint. OAuth credentials are encrypted at rest and are never returned by the Settings API.
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
                      Choose how the Cycle Agent authenticates to its model provider.
                    </p>
                  </div>
                  <label className="flex items-center gap-2 text-xs text-slate-600">
                    <input
                      type="checkbox"
                      checked={draft.enabled}
                      onChange={(event) => setDraft({ ...draft, enabled: event.target.checked })}
                      className="size-4 accent-cyan-500"
                    />
                    Enabled
                  </label>
                </div>

                <div className="mt-4 grid gap-2 md:grid-cols-3">
                  {providerOptions.map((provider) => (
                    <button
                      key={provider.id}
                      type="button"
                      onClick={() => {
                        setDraft({ ...draft, provider_type: provider.id, model: '' })
                        setGrokFlow(null)
                        testProvider.reset()
                      }}
                      className={
                        'rounded-xl border px-3 py-3 text-left transition ' +
                        (draft.provider_type === provider.id
                          ? 'border-sky-500 bg-sky-50'
                          : 'border-slate-200 bg-white hover:border-slate-300')
                      }
                    >
                      <span className="text-xs font-semibold text-slate-800">{provider.title}</span>
                      <span className="mt-1 block text-[11px] leading-4 text-slate-600">{provider.description}</span>
                    </button>
                  ))}
                </div>

                {draft.provider_type === 'openai_compatible' ? (
                  <div className="mt-4 grid gap-4 md:grid-cols-2">
                    <Field label="OpenAI-compatible Base URL">
                      <input
                        value={draft.provider_base_url}
                        onChange={(event) => setDraft({ ...draft, provider_base_url: event.target.value })}
                        placeholder="https://api.example.com/v1"
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
                  </div>
                ) : (
                  <div className="mt-4 rounded-xl border border-slate-200 bg-slate-50 p-3">
                    <div className="flex flex-wrap items-center justify-between gap-3">
                      <div>
                        <p className="text-xs font-semibold text-slate-800">
                          {draft.provider_type === 'chatgpt' ? 'ChatGPT account' : 'Grok account'}
                        </p>
                        <p className="mt-1 text-[11px] text-slate-600">
                          {!subscriptionStatus?.available
                            ? authReason(subscriptionStatus?.reason)
                            : subscriptionStatus.connected
                              ? 'Connected. Inference uses this account’s subscription entitlement.'
                              : 'Not connected.'}
                        </p>
                      </div>
                      {subscriptionStatus?.connected ? (
                        <button
                          type="button"
                          onClick={() => subscriptionProvider && disconnect.mutate(subscriptionProvider)}
                          disabled={disconnect.isPending}
                          className="inline-flex h-8 items-center gap-1.5 rounded-lg border border-slate-300 px-2.5 text-xs text-slate-700 disabled:opacity-40"
                        >
                          <LogOut className="size-3.5" /> Disconnect
                        </button>
                      ) : draft.provider_type === 'chatgpt' ? (
                        <button
                          type="button"
                          onClick={() => connectChatGPT.mutate()}
                          disabled={!subscriptionStatus?.available || connectChatGPT.isPending}
                          className="inline-flex h-8 items-center gap-1.5 rounded-lg bg-slate-900 px-3 text-xs font-semibold text-white disabled:opacity-40"
                        >
                          {connectChatGPT.isPending ? <LoaderCircle className="size-3.5 animate-spin" /> : <ExternalLink className="size-3.5" />}
                          Continue with ChatGPT
                        </button>
                      ) : (
                        <button
                          type="button"
                          onClick={() => connectGrok.mutate()}
                          disabled={!subscriptionStatus?.available || connectGrok.isPending}
                          className="inline-flex h-8 items-center gap-1.5 rounded-lg bg-slate-900 px-3 text-xs font-semibold text-white disabled:opacity-40"
                        >
                          {connectGrok.isPending ? <LoaderCircle className="size-3.5 animate-spin" /> : <ExternalLink className="size-3.5" />}
                          Continue with Grok
                        </button>
                      )}
                    </div>

                    {draft.provider_type === 'grok' && grokFlow ? (
                      <div className="mt-3 rounded-lg border border-sky-200 bg-sky-50 p-3 text-xs text-slate-700">
                        <p>
                          Enter code <strong className="font-mono">{grokFlow.user_code}</strong> on the xAI sign-in page.
                        </p>
                        <div className="mt-2 flex flex-wrap gap-2">
                          <a
                            href={grokFlow.verification_url}
                            target="_blank"
                            rel="noreferrer"
                            className="inline-flex h-8 items-center gap-1.5 rounded-lg border border-sky-300 px-2.5"
                          >
                            <ExternalLink className="size-3.5" /> Open xAI
                          </a>
                          <button
                            type="button"
                            onClick={() => pollGrok.mutate(grokFlow.flow_id)}
                            disabled={pollGrok.isPending}
                            className="inline-flex h-8 items-center gap-1.5 rounded-lg bg-sky-700 px-2.5 font-semibold text-white disabled:opacity-40"
                          >
                            {pollGrok.isPending ? <LoaderCircle className="size-3.5 animate-spin" /> : null}
                            Check connection
                          </button>
                        </div>
                      </div>
                    ) : null}
                  </div>
                )}

                <div className="mt-4 grid gap-4 md:grid-cols-2">
                  <Field label="Model">
                    {subscriptionProvider && subscriptionStatus?.available && subscriptionStatus.connected && models.data?.length ? (
                      <select
                        value={draft.model}
                        onChange={(event) => setDraft({ ...draft, model: event.target.value })}
                        className={inputClass}
                      >
                        <option value="">Select a model</option>
                        {models.data.map((model) => (
                          <option key={model.id} value={model.id}>
                            {model.name}
                          </option>
                        ))}
                      </select>
                    ) : (
                      <input
                        value={draft.model}
                        onChange={(event) => setDraft({ ...draft, model: event.target.value })}
                        placeholder={
                          subscriptionProvider
                            ? subscriptionStatus?.available && subscriptionStatus.connected
                              ? 'Model ID'
                              : 'Connect the account first'
                            : 'gpt-compatible-model'
                        }
                        disabled={Boolean(subscriptionProvider && (!subscriptionStatus?.available || !subscriptionStatus.connected))}
                        className={inputClass}
                      />
                    )}
                    {models.isPending && subscriptionStatus?.available && subscriptionStatus.connected ? (
                      <span className="mt-1 block text-[11px] text-slate-500">Loading account model catalog…</span>
                    ) : null}
                    {models.isError ? (
                      <span className="mt-1 block text-[11px] text-amber-700">
                        Model catalog could not be loaded; enter a model ID manually.
                      </span>
                    ) : null}
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
                    disabled={saveSettings.isPending || (draft.enabled && !providerReady)}
                    onClick={() => saveSettings.mutate()}
                    className="inline-flex h-9 items-center gap-2 rounded-xl bg-cyan-400 px-4 text-xs font-semibold text-slate-950 disabled:opacity-40"
                  >
                    <Save className="size-3.5" /> Save runtime
                  </button>
                  <button
                    type="button"
                    disabled={testProvider.isPending || !providerReady}
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
                          className={'shrink-0 text-[10px] uppercase tracking-wider ' + (connection.enabled ? 'text-emerald-700' : 'text-slate-500')}
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

function authReason(reason?: string) {
  switch (reason) {
    case 'secret_key_required':
      return 'Server setup is missing CYCLEDOCK_MODEL_SECRET_KEY.'
    case 'public_url_required':
      return 'ChatGPT sign-in requires CYCLEDOCK_PUBLIC_URL.'
    case 'chatgpt_client_id_required':
      return 'Hosted ChatGPT plan usage requires an approved CYCLEDOCK_CHATGPT_CLIENT_ID.'
    default:
      return 'Model account sign-in is unavailable.'
  }
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
          {typeof result.tool_count === 'number' ? ` 路 ${result.tool_count} tools` : ''}
          {` 路 ${result.duration_ms} ms`}
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