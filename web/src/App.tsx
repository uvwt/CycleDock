import { Activity, Columns3, RefreshCw } from 'lucide-react'
import type { ReactNode } from 'react'

function App() {
  return (
    <main className="min-h-screen bg-slate-950 text-slate-100">
      <div className="mx-auto flex min-h-screen max-w-5xl items-center px-6 py-16">
        <section className="w-full rounded-3xl border border-slate-800 bg-slate-900/70 p-8 shadow-2xl shadow-black/20 sm:p-12">
          <div className="mb-10 flex items-center gap-3">
            <div className="rounded-2xl bg-cyan-400/10 p-3 text-cyan-300">
              <RefreshCw className="size-6" aria-hidden="true" />
            </div>
            <div>
              <p className="text-sm font-medium uppercase tracking-[0.2em] text-cyan-300">
                CycleDock
              </p>
              <h1 className="text-3xl font-semibold tracking-tight sm:text-4xl">
                Human + Agent work cycles
              </h1>
            </div>
          </div>

          <p className="max-w-3xl text-base leading-7 text-slate-300 sm:text-lg">
            Project 是看板，Card 保存长期任务知识；每个新方向形成 Trigger，
            每次执行形成独立 AgentRun。当前工程基线已经为 Phase 1
            的任务状态机准备好前端运行环境。
          </p>

          <div className="mt-10 grid gap-4 sm:grid-cols-3">
            <FoundationCard
              icon={<Columns3 className="size-5" aria-hidden="true" />}
              title="Project → Card"
              detail="Project 直接承载 Kanban 视图，不引入独立 Board 领域。"
            />
            <FoundationCard
              icon={<RefreshCw className="size-5" aria-hidden="true" />}
              title="Trigger → Run"
              detail="Trigger 是不可变方向与队列项，Run 只保存本轮执行状态。"
            />
            <FoundationCard
              icon={<Activity className="size-5" aria-hidden="true" />}
              title="Ongoing"
              detail="Schedule 产生 Trigger，Health 由最新 Check-in 派生。"
            />
          </div>
        </section>
      </div>
    </main>
  )
}

function FoundationCard({
  icon,
  title,
  detail,
}: {
  icon: ReactNode
  title: string
  detail: string
}) {
  return (
    <article className="rounded-2xl border border-slate-800 bg-slate-950/60 p-5">
      <div className="mb-4 text-cyan-300">{icon}</div>
      <h2 className="font-semibold text-slate-100">{title}</h2>
      <p className="mt-2 text-sm leading-6 text-slate-400">{detail}</p>
    </article>
  )
}

export default App
