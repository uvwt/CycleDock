import { useQuery } from '@tanstack/react-query'
import { LoaderCircle } from 'lucide-react'
import { lazy, Suspense } from 'react'
import { Navigate, Route, Routes } from 'react-router-dom'
import { APIError, api } from './api'
import { LoginPage } from './pages/LoginPage'
import { queryKeys } from './query'

const WorkspacePage = lazy(() =>
  import('./pages/WorkspacePage').then((module) => ({ default: module.WorkspacePage })),
)

export default function App() {
  const me = useQuery({
    queryKey: queryKeys.me,
    queryFn: api.me,
  })

  if (me.isPending) {
    return (
      <main className="grid min-h-screen place-items-center bg-slate-950 text-slate-200">
        <div className="flex items-center gap-3 text-sm text-slate-400">
          <LoaderCircle className="size-5 animate-spin" />
          Loading CycleDock…
        </div>
      </main>
    )
  }

  if (me.error instanceof APIError && me.error.status === 401) {
    return <LoginPage />
  }

  if (me.isError || !me.data) {
    return (
      <main className="grid min-h-screen place-items-center bg-slate-950 px-6 text-slate-200">
        <div className="max-w-md rounded-2xl border border-rose-900/60 bg-rose-950/20 p-6">
          <h1 className="font-semibold text-rose-200">CycleDock could not load your session.</h1>
          <p className="mt-2 text-sm text-rose-300/80">
            {me.error instanceof Error ? me.error.message : 'Unknown session error.'}
          </p>
        </div>
      </main>
    )
  }

  const workspace = (
    <Suspense
      fallback={
        <main className="grid min-h-screen place-items-center bg-slate-950 text-slate-400">
          <LoaderCircle className="size-5 animate-spin" />
        </main>
      }
    >
      <WorkspacePage identity={me.data} />
    </Suspense>
  )

  return (
    <Routes>
      <Route path="/projects/:projectID/cards/:cardID" element={workspace} />
      <Route path="/projects/:projectID" element={workspace} />
      <Route path="/" element={workspace} />
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  )
}
