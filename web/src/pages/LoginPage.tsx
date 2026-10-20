import { useMutation, useQuery } from '@tanstack/react-query'
import { ArrowRight, LoaderCircle, RefreshCw, ShieldCheck } from 'lucide-react'
import { useForm } from 'react-hook-form'
import { toast } from 'sonner'
import { z } from 'zod'
import { APIError, api } from '../api'
import { queryClient, queryKeys } from '../query'

const loginSchema = z.object({
  password: z.string().min(1, 'Enter the administrator password.'),
})

type LoginValues = z.infer<typeof loginSchema>

function authErrorMessage(code: string | null) {
  switch (code) {
    case 'access_denied':
      return 'NexusDock sign-in was canceled.'
    case 'invalid_state':
      return 'This sign-in request expired or was already used. Start again here.'
    case 'provider_error':
      return 'NexusDock sign-in could not be completed. Please try again.'
    default:
      return null
  }
}

export function LoginPage() {
  const methods = useQuery({
    queryKey: queryKeys.loginMethods,
    queryFn: api.loginMethods,
    staleTime: 60_000,
  })
  const form = useForm<LoginValues>({ defaultValues: { password: '' } })
  const login = useMutation({
    mutationFn: async (values: LoginValues) => {
      const parsed = loginSchema.parse(values)
      return api.login(parsed.password)
    },
    onSuccess: (identity) => {
      queryClient.setQueryData(queryKeys.me, identity)
    },
    onError: (error) => {
      toast.error(
        error instanceof APIError && error.status === 401
          ? 'That password is not valid.'
          : error instanceof Error
            ? error.message
            : 'Sign in failed.',
      )
    },
  })

  const params = new URLSearchParams(window.location.search)
  const authError = authErrorMessage(params.get('auth_error'))
  const providers = methods.data?.providers ?? []
  const passwordEnabled = methods.data?.password ?? false

  return (
    <main className="grid min-h-screen bg-slate-950 text-slate-100 lg:grid-cols-[1.08fr_0.92fr]">
      <section className="hidden border-r border-slate-800 bg-[radial-gradient(circle_at_20%_20%,rgba(34,211,238,0.12),transparent_34%)] p-12 lg:flex lg:flex-col lg:justify-between">
        <div className="flex items-center gap-3">
          <span className="grid size-10 place-items-center rounded-2xl bg-cyan-400 text-sm font-black text-slate-950">C</span>
          <span className="font-semibold tracking-tight">CycleDock</span>
        </div>
        <div className="max-w-xl">
          <p className="mb-5 text-xs font-semibold uppercase tracking-[0.22em] text-cyan-300">Shared work state</p>
          <h1 className="text-5xl font-semibold leading-[1.04] tracking-[-0.045em]">
            One board for people
            <br />
            and AI agents.
          </h1>
          <p className="mt-6 max-w-lg text-base leading-7 text-slate-400">
            Cards keep durable task context. Triggers capture new directions. Runs stay isolated so old
            execution state never silently leaks into the next cycle.
          </p>
        </div>
        <div className="flex items-center gap-2 text-xs text-slate-500">
          <ShieldCheck className="size-4" />
          Human sessions and agent OAuth stay separate by design.
        </div>
      </section>

      <section className="flex items-center justify-center px-6 py-12">
        <div className="w-full max-w-sm">
          <div className="mb-9 flex items-center gap-3 lg:hidden">
            <span className="grid size-10 place-items-center rounded-2xl bg-cyan-400 text-sm font-black text-slate-950">C</span>
            <span className="font-semibold">CycleDock</span>
          </div>
          <p className="text-sm font-medium text-cyan-300">Welcome</p>
          <h2 className="mt-2 text-3xl font-semibold tracking-[-0.035em]">Sign in to your workspace</h2>
          <p className="mt-3 text-sm leading-6 text-slate-400">
            Hosted CycleDock uses NexusDock. Self-hosted instances can use their local administrator password.
          </p>

          {authError ? (
            <div className="mt-6 rounded-xl border border-rose-900 bg-rose-950/40 px-4 py-3 text-sm text-rose-200">{authError}</div>
          ) : null}

          {methods.isPending ? (
            <div className="mt-8 flex items-center gap-2 text-sm text-slate-500">
              <LoaderCircle className="size-4 animate-spin" /> Loading sign-in methods…
            </div>
          ) : null}

          {providers.length > 0 ? (
            <div className="mt-8 space-y-3">
              {providers.map((provider) => (
                <a
                  key={provider.id}
                  href={provider.start_url}
                  className="flex h-11 items-center justify-center gap-2 rounded-xl bg-cyan-400 px-4 text-sm font-semibold text-slate-950 transition hover:bg-cyan-300"
                >
                  <RefreshCw className="size-4" />
                  Continue with {provider.name}
                </a>
              ))}
            </div>
          ) : null}

          {providers.length > 0 && passwordEnabled ? (
            <div className="my-6 flex items-center gap-3 text-[11px] uppercase tracking-[0.14em] text-slate-600">
              <span className="h-px flex-1 bg-slate-800" /> or <span className="h-px flex-1 bg-slate-800" />
            </div>
          ) : null}

          {passwordEnabled ? (
            <form className={providers.length > 0 ? 'space-y-4' : 'mt-8 space-y-4'} onSubmit={form.handleSubmit((values) => login.mutate(values))}>
              <div>
                <label htmlFor="password" className="mb-2 block text-xs font-medium text-slate-300">Administrator password</label>
                <input
                  id="password"
                  type="password"
                  autoComplete="current-password"
                  {...form.register('password')}
                  className="h-11 w-full rounded-xl border border-slate-700 bg-slate-900 px-3.5 text-sm outline-none focus:border-cyan-400 focus:ring-2 focus:ring-cyan-400/10"
                />
              </div>
              <button type="submit" disabled={login.isPending} className="flex h-11 w-full items-center justify-center gap-2 rounded-xl bg-slate-100 px-4 text-sm font-semibold text-slate-950 transition hover:bg-white disabled:opacity-50">
                {login.isPending ? 'Signing in…' : 'Sign in'}
                {!login.isPending ? <ArrowRight className="size-4" /> : null}
              </button>
            </form>
          ) : null}

          {!methods.isPending && !passwordEnabled && providers.length === 0 ? (
            <div className="mt-8 rounded-xl border border-amber-900 bg-amber-950/30 px-4 py-3 text-sm text-amber-200">
              No human sign-in method is configured on this CycleDock instance.
            </div>
          ) : null}
        </div>
      </section>
    </main>
  )
}
