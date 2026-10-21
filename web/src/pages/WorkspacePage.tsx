import * as Dialog from '@radix-ui/react-dialog'
import { useMutation, useQuery } from '@tanstack/react-query'
import {
  Bot,
  ChevronDown,
  CircleAlert,
  CirclePlus,
  FolderKanban,
  Languages,
  LogOut,
  Pencil,
  Plus,
  RefreshCw,
  Settings2,
  X,
} from 'lucide-react'
import { lazy, Suspense, useEffect, useMemo, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { toast } from 'sonner'
import { api, type Card, type Identity, type Project, type TodoState } from '../api'
import { useI18n } from '../i18n-context'
import { queryClient, queryKeys } from '../query'

const Board = lazy(() => import('../components/Board').then((module) => ({ default: module.Board })))
const CardDetailDialog = lazy(() =>
  import('../components/CardDetailDialog').then((module) => ({ default: module.CardDetailDialog })),
)
const AgentSettingsDialog = lazy(() =>
  import('../components/AgentSettingsDialog').then((module) => ({ default: module.AgentSettingsDialog })),
)

export function WorkspacePage({ identity }: { identity: Identity }) {
  const { locale, setLocale, t } = useI18n()
  const navigate = useNavigate()
  const params = useParams<{ projectID?: string; cardID?: string }>()
  const projects = useQuery({
    queryKey: queryKeys.projects,
    queryFn: api.projects,
  })

  const activeProjectID = useMemo(() => {
    if (params.projectID && projects.data?.some((project) => project.id === params.projectID)) {
      return params.projectID
    }
    return projects.data?.[0]?.id
  }, [params.projectID, projects.data])

  useEffect(() => {
    if (!params.projectID && activeProjectID) {
      navigate(`/projects/${activeProjectID}`, { replace: true })
    }
  }, [activeProjectID, navigate, params.projectID])

  const activeProject = projects.data?.find((project) => project.id === activeProjectID)
  const cards = useQuery({
    queryKey: queryKeys.cards(activeProjectID ?? ''),
    queryFn: () => api.projectCards(activeProjectID!),
    enabled: Boolean(activeProjectID),
  })

  const move = useMutation({
    mutationFn: ({ cardID, state }: { cardID: string; state: TodoState }) => api.moveCard(cardID, state),
    onSuccess: async (_, variables) => {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: queryKeys.cards(activeProjectID ?? '') }),
        queryClient.invalidateQueries({ queryKey: queryKeys.card(variables.cardID) }),
      ])
    },
    onError: (error) => toast.error(error instanceof Error ? error.message : 'Could not move card'),
  })

  const logout = useMutation({
    mutationFn: api.logout,
    onSuccess: () => {
      queryClient.clear()
      window.location.assign('/')
    },
    onError: (error) => toast.error(error instanceof Error ? error.message : 'Sign out failed'),
  })

  const [createProjectOpen, setCreateProjectOpen] = useState(false)
  const [projectSettingsOpen, setProjectSettingsOpen] = useState(false)
  const [agentSettingsOpen, setAgentSettingsOpen] = useState(() => {
    const search = new URLSearchParams(window.location.search)
    return search.has('model_auth') || search.has('agent_connections')
  })

  useEffect(() => {
    const search = new URLSearchParams(window.location.search)
    const modelResult = search.get('model_auth')
    const nexusDockResult = search.get('agent_connections')
    if (!modelResult && !nexusDockResult) return

    if (modelResult === 'chatgpt_connected') {
      toast.success('ChatGPT account connected')
      void queryClient.invalidateQueries({ queryKey: queryKeys.modelAuth })
    } else if (modelResult === 'chatgpt_denied') {
      toast.error('ChatGPT sign-in was cancelled')
    } else if (modelResult) {
      toast.error('ChatGPT sign-in could not be completed')
    }

    if (nexusDockResult === 'nexusdock-connected') {
      toast.success('NexusDock remote devices connected')
      void queryClient.invalidateQueries({ queryKey: queryKeys.agentNexusDock })
    } else if (nexusDockResult === 'nexusdock-denied') {
      toast.info('NexusDock connection was not authorized')
    } else if (nexusDockResult) {
      toast.error('Could not connect NexusDock remote devices')
    }

    search.delete('model_auth')
    search.delete('agent_connections')
    const query = search.toString()
    window.history.replaceState(
      window.history.state,
      '',
      window.location.pathname + (query ? `?${query}` : '') + window.location.hash,
    )
  }, [])

  function chooseProject(projectID: string) {
    navigate(`/projects/${projectID}`)
  }

  function openCard(card: Card) {
    if (activeProjectID) {
      navigate(`/projects/${activeProjectID}/cards/${card.id}`)
    }
  }

  const createDiscussion = useMutation({
    mutationFn: () => api.createDiscussion(activeProjectID!),
    onSuccess: async (card) => {
      await queryClient.invalidateQueries({ queryKey: queryKeys.cards(activeProjectID ?? '') })
      navigate(`/projects/${activeProjectID}/cards/${card.id}`)
    },
    onError: (error) => toast.error(error instanceof Error ? error.message : 'Could not create discussion'),
  })

  return (
    <main className="flex min-h-screen bg-slate-50 text-slate-900">
      <aside className="hidden w-64 shrink-0 border-r border-slate-200 bg-white/95 lg:flex lg:flex-col">
        <div className="flex h-16 items-center gap-3 border-b border-slate-200 px-5">
          <span className="grid size-9 place-items-center rounded-xl bg-cyan-400 text-sm font-black text-slate-950">C</span>
          <div>
            <p className="text-sm font-semibold">CycleDock</p>
            <p className="text-[10px] uppercase tracking-[0.12em] text-slate-600">Human + Agent</p>
          </div>
        </div>

        <div className="flex-1 overflow-y-auto p-3">
          <div className="mb-2 flex items-center justify-between px-2">
            <p className="text-[11px] font-semibold uppercase tracking-[0.12em] text-slate-600">{t('nav.projects')}</p>
            <button
              type="button"
              onClick={() => setCreateProjectOpen(true)}
              className="grid size-7 place-items-center rounded-lg text-slate-600 transition hover:bg-white hover:text-sky-700"
              title="Create project"
            >
              <Plus className="size-3.5" />
            </button>
          </div>
          <nav className="space-y-1">
            {(projects.data ?? []).map((project) => (
              <button
                type="button"
                key={project.id}
                onClick={() => chooseProject(project.id)}
                className={`flex w-full items-center gap-2 rounded-xl px-3 py-2.5 text-left text-sm transition ${
                  project.id === activeProjectID
                    ? 'bg-white text-slate-900'
                    : 'text-slate-600 hover:bg-white hover:text-slate-700'
                }`}
              >
                <FolderKanban className="size-4 shrink-0" />
                <span className="truncate">{project.name}</span>
              </button>
            ))}
          </nav>
        </div>

        <div className="border-t border-slate-200 p-3">
          <div className="rounded-xl bg-white px-3 py-2.5">
            <p className="truncate text-xs font-medium text-slate-700">{identity.display_name}</p>
            <p className="mt-0.5 truncate text-[10px] text-slate-600">{identity.workspace.name}</p>
          </div>
          <button
            type="button"
            onClick={() => logout.mutate()}
            className="mt-1 flex w-full items-center gap-2 rounded-xl px-3 py-2 text-xs text-slate-600 transition hover:bg-white hover:text-slate-700"
          >
            <LogOut className="size-3.5" /> {t('nav.signOut')}
          </button>
        </div>
      </aside>

      <section className="min-w-0 flex-1">
        <header className="flex h-16 items-center justify-between border-b border-slate-200 px-4 md:px-6">
          <div className="min-w-0">
            <div className="flex items-center gap-2 lg:hidden">
              <RefreshCw className="size-4 text-sky-700" />
              <span className="text-sm font-semibold">CycleDock</span>
            </div>
            <div className="hidden lg:block">
              <p className="truncate text-sm font-semibold">{activeProject?.name ?? 'Projects'}</p>
              <p className="mt-0.5 truncate text-[11px] text-slate-600">
                {activeProject?.description || identity.workspace.name}
              </p>
            </div>
          </div>
          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={() => setLocale(locale === 'en' ? 'zh-CN' : 'en')}
              className="flex h-9 items-center gap-1.5 rounded-xl border border-slate-200 px-2.5 text-[11px] font-medium text-slate-600 transition hover:border-slate-300 hover:text-slate-800"
              title={locale === 'en' ? t('locale.switchToChinese') : t('locale.switchToEnglish')}
              aria-label={locale === 'en' ? t('locale.switchToChinese') : t('locale.switchToEnglish')}
            >
              <Languages className="size-3.5" />
              {locale === 'en' ? '中文' : 'EN'}
            </button>
            {identity.role === 'owner' ? (
              <button
                type="button"
                onClick={() => setAgentSettingsOpen(true)}
                className="grid size-9 place-items-center rounded-xl border border-slate-200 text-slate-600 transition hover:border-slate-300 hover:text-sky-700"
                title={t('nav.agent')}
                aria-label={t('nav.agent')}
              >
                <Bot className="size-4" />
              </button>
            ) : null}
            {activeProject ? (
              <>
                <button
                  type="button"
                  onClick={() => setProjectSettingsOpen(true)}
                  className="grid size-9 place-items-center rounded-xl border border-slate-200 text-slate-600 transition hover:border-slate-300 hover:text-slate-700"
                  title={t('nav.projectSettings')}
                  aria-label={t('nav.projectSettings')}
                >
                  <Settings2 className="size-4" />
                </button>
                <button
                  type="button"
                  onClick={() => createDiscussion.mutate()}
                  disabled={createDiscussion.isPending}
                  className="flex h-9 items-center gap-2 rounded-xl bg-cyan-400 px-3.5 text-xs font-semibold text-slate-950 transition hover:bg-cyan-300 disabled:opacity-50"
                >
                  <CirclePlus className="size-4" /> {t('nav.newDiscussion')}
                </button>
              </>
            ) : (
              <button
                type="button"
                onClick={() => setCreateProjectOpen(true)}
                className="flex h-9 items-center gap-2 rounded-xl bg-cyan-400 px-3.5 text-xs font-semibold text-slate-950"
              >
                <Plus className="size-4" /> {t('nav.newProject')}
              </button>
            )}
          </div>
        </header>

        <div className="min-w-0 overflow-x-hidden px-4 py-5 md:px-6">
          {projects.isPending ? (
            <LoadingState message={t('state.loadingProjects')} />
          ) : projects.isError ? (
            <LoadError
              message={projects.error instanceof Error ? projects.error.message : t('state.projectsError')}
              retryLabel={t('action.retry')}
              onRetry={() => void projects.refetch()}
            />
          ) : !activeProject ? (
            <EmptyWorkspace onCreate={() => setCreateProjectOpen(true)} />
          ) : cards.isPending ? (
            <LoadingState message={t('state.loadingBoard')} />
          ) : cards.isError ? (
            <LoadError
              message={cards.error instanceof Error ? cards.error.message : t('state.cardsError')}
              retryLabel={t('action.retry')}
              onRetry={() => void cards.refetch()}
            />
          ) : (
            <>
              <div className="mb-5 flex items-start justify-between gap-5 lg:hidden">
                <div>
                  <ProjectPicker
                    projects={projects.data ?? []}
                    activeProjectID={activeProjectID}
                    onChoose={chooseProject}
                  />
                  <p className="mt-2 max-w-xl text-xs leading-5 text-slate-600">{activeProject.description}</p>
                </div>
              </div>
              <Suspense
                fallback={
                  <div className="min-h-[420px] rounded-2xl border border-slate-200 bg-white" />
                }
              >
                <Board
                  cards={cards.data ?? []}
                  onOpen={openCard}
                  onMove={(cardID, state) => move.mutate({ cardID, state })}
                />
              </Suspense>
            </>
          )}
        </div>
      </section>

      <CreateProjectDialog open={createProjectOpen} onOpenChange={setCreateProjectOpen} />
      {agentSettingsOpen ? (
        <Suspense fallback={null}>
          <AgentSettingsDialog open={agentSettingsOpen} onOpenChange={setAgentSettingsOpen} />
        </Suspense>
      ) : null}
      {activeProject ? (
        <>
          <ProjectSettingsDialog
            key={activeProject.id}
            project={activeProject}
            open={projectSettingsOpen}
            onOpenChange={setProjectSettingsOpen}
          />
        </>
      ) : null}
      {activeProjectID && params.cardID ? (
        <Suspense fallback={null}>
          <CardDetailDialog
            projectID={activeProjectID}
            cardID={params.cardID}
            onClose={() => navigate(`/projects/${activeProjectID}`)}
          />
        </Suspense>
      ) : null}
    </main>
  )
}

function LoadingState({ message }: { message: string }) {
  return (
    <div className="flex items-center justify-center gap-2 py-20 text-sm text-slate-600" role="status" aria-live="polite">
      <RefreshCw className="size-4 animate-spin" />
      {message}
    </div>
  )
}

function LoadError({
  message,
  retryLabel,
  onRetry,
}: {
  message: string
  retryLabel: string
  onRetry: () => void
}) {
  return (
    <div className="mx-auto my-16 max-w-md rounded-2xl border border-rose-200 bg-rose-50 p-5 text-center" role="alert">
      <CircleAlert className="mx-auto size-6 text-rose-700" />
      <p className="mt-3 text-sm text-rose-800">{message}</p>
      <button
        type="button"
        onClick={onRetry}
        className="mt-4 inline-flex h-9 items-center gap-2 rounded-xl border border-rose-200 px-3 text-xs font-semibold text-rose-800 transition hover:border-rose-400"
      >
        <RefreshCw className="size-3.5" /> {retryLabel}
      </button>
    </div>
  )
}

function EmptyWorkspace({ onCreate }: { onCreate: () => void }) {
  return (
    <div className="mx-auto mt-24 max-w-lg rounded-3xl border border-dashed border-slate-200 p-10 text-center">
      <FolderKanban className="mx-auto size-9 text-slate-700" />
      <h1 className="mt-5 text-xl font-semibold">Create your first project</h1>
      <p className="mt-2 text-sm leading-6 text-slate-600">
        A Project is the board. Cards hold durable work context, while Triggers and Runs capture each execution cycle.
      </p>
      <button
        type="button"
        onClick={onCreate}
        className="mt-6 inline-flex h-10 items-center gap-2 rounded-xl bg-cyan-400 px-4 text-sm font-semibold text-slate-950"
      >
        <Plus className="size-4" /> Create project
      </button>
    </div>
  )
}

function ProjectPicker({
  projects,
  activeProjectID,
  onChoose,
}: {
  projects: Project[]
  activeProjectID?: string
  onChoose: (projectID: string) => void
}) {
  return (
    <div className="relative">
      <select
        value={activeProjectID}
        onChange={(event) => onChoose(event.target.value)}
        className="h-9 appearance-none rounded-xl border border-slate-200 bg-white py-0 pl-3 pr-9 text-sm font-medium outline-none"
      >
        {projects.map((project) => (
          <option key={project.id} value={project.id}>{project.name}</option>
        ))}
      </select>
      <ChevronDown className="pointer-events-none absolute right-3 top-2.5 size-4 text-slate-600" />
    </div>
  )
}

function CreateProjectDialog({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const navigate = useNavigate()
  const create = useMutation({
    mutationFn: () => api.createProject({ name: name.trim(), description: description.trim(), instructions: '' }),
    onSuccess: async (project) => {
      await queryClient.invalidateQueries({ queryKey: queryKeys.projects })
      setName('')
      setDescription('')
      onOpenChange(false)
      navigate(`/projects/${project.id}`)
    },
    onError: (error) => toast.error(error instanceof Error ? error.message : 'Could not create project'),
  })

  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-40 bg-slate-900/35" />
        <Dialog.Content className="fixed left-1/2 top-1/2 z-50 w-[min(92vw,480px)] -translate-x-1/2 -translate-y-1/2 rounded-2xl border border-slate-200 bg-slate-50 p-5 shadow-2xl outline-none">
          <Dialog.Title className="text-lg font-semibold">New project</Dialog.Title>
          <Dialog.Description className="mt-1 text-sm text-slate-600">The project itself is the board—there is no extra Board layer.</Dialog.Description>
          <label className="mt-5 block text-xs font-medium text-slate-600">Name</label>
          <input value={name} onChange={(event) => setName(event.target.value)} autoFocus className="mt-2 h-10 w-full rounded-xl border border-slate-200 bg-white px-3 text-sm outline-none focus:border-cyan-500/60" />
          <label className="mt-4 block text-xs font-medium text-slate-600">Description</label>
          <textarea value={description} onChange={(event) => setDescription(event.target.value)} rows={3} className="mt-2 w-full resize-none rounded-xl border border-slate-200 bg-white px-3 py-2 text-sm outline-none focus:border-cyan-500/60" />
          <button type="button" disabled={!name.trim() || create.isPending} onClick={() => create.mutate()} className="mt-5 h-10 w-full rounded-xl bg-cyan-400 text-sm font-semibold text-slate-950 disabled:opacity-40">
            Create project
          </button>
          <Dialog.Close aria-label="Close" className="absolute right-4 top-4 grid size-8 place-items-center rounded-lg text-slate-600 hover:bg-white hover:text-slate-700"><X className="size-4" /></Dialog.Close>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  )
}

function ProjectSettingsDialog({
  project,
  open,
  onOpenChange,
}: {
  project: Project
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const [name, setName] = useState(project.name)
  const [description, setDescription] = useState(project.description)
  const [instructions, setInstructions] = useState(project.instructions)

  const update = useMutation({
    mutationFn: () => api.updateProject(project.id, {
      name: name.trim(),
      description: description.trim(),
      instructions: instructions.trim(),
    }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: queryKeys.projects })
      onOpenChange(false)
      toast.success('Project updated')
    },
    onError: (error) => toast.error(error instanceof Error ? error.message : 'Could not update project'),
  })

  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-40 bg-slate-900/35" />
        <Dialog.Content className="fixed left-1/2 top-1/2 z-50 w-[min(92vw,560px)] -translate-x-1/2 -translate-y-1/2 rounded-2xl border border-slate-200 bg-slate-50 p-5 shadow-2xl outline-none">
          <Dialog.Title className="flex items-center gap-2 text-lg font-semibold"><Pencil className="size-4 text-sky-700" /> Project settings</Dialog.Title>
          <Dialog.Description className="mt-1 text-sm text-slate-600">Project instructions are durable context for future Agent runs.</Dialog.Description>
          <label className="mt-5 block text-xs font-medium text-slate-600">Name</label>
          <input value={name} onChange={(event) => setName(event.target.value)} className="mt-2 h-10 w-full rounded-xl border border-slate-200 bg-white px-3 text-sm outline-none focus:border-cyan-500/60" />
          <label className="mt-4 block text-xs font-medium text-slate-600">Description</label>
          <textarea value={description} onChange={(event) => setDescription(event.target.value)} rows={3} className="mt-2 w-full resize-none rounded-xl border border-slate-200 bg-white px-3 py-2 text-sm outline-none focus:border-cyan-500/60" />
          <label className="mt-4 block text-xs font-medium text-slate-600">Agent instructions</label>
          <textarea value={instructions} onChange={(event) => setInstructions(event.target.value)} rows={5} className="mt-2 w-full resize-none rounded-xl border border-slate-200 bg-white px-3 py-2 text-sm outline-none focus:border-cyan-500/60" />
          <button type="button" disabled={!name.trim() || update.isPending} onClick={() => update.mutate()} className="mt-5 h-10 w-full rounded-xl bg-slate-100 text-sm font-semibold text-slate-950 disabled:opacity-40">
            Save project
          </button>
          <Dialog.Close className="absolute right-4 top-4 grid size-8 place-items-center rounded-lg text-slate-600 hover:bg-white hover:text-slate-700"><X className="size-4" /></Dialog.Close>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  )
}
