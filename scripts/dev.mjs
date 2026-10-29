import { spawn } from 'node:child_process'
import { chmodSync, constants, copyFileSync, existsSync } from 'node:fs'
import { createServer } from 'node:net'
import { resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

process.chdir(fileURLToPath(new URL('..', import.meta.url)))
const airVersion = 'v1.67.4'
const children = new Map()
let stopping = false

// 每个命令独立建进程组，退出时连同 Air 编译出的后端一起回收。
function run(command, args, { keepGroup = false, ...options } = {}) {
  const child = spawn(command, args, { stdio: 'inherit', detached: true, ...options })
  const done = new Promise((resolve, reject) => {
    child.once('error', reject)
    child.once('exit', (code, signal) => {
      if (!keepGroup) children.delete(child)
      if (code === 0) resolve()
      else reject(new Error(`${command} 退出：${signal ?? code}`))
    })
  })
  children.set(child, done)
  // 开发服务保留进程组直到统一退出，避免父进程先退出时遗漏子进程。
  // 一次性命令完成后移除，防止长时间开发后 PID 复用导致误发信号。
  done.catch(() => {})
  return done
}

function signalChildren(signal) {
  for (const child of children.keys()) {
    if (!child.pid) continue
    try {
      process.kill(-child.pid, signal)
    } catch (error) {
      if (error.code !== 'ESRCH') throw error
    }
  }
}

async function stop(code) {
  if (stopping) return
  stopping = true
  signalChildren('SIGINT')
  let timeout
  await Promise.race([
    Promise.allSettled(children.values()),
    new Promise(resolve => { timeout = setTimeout(resolve, 5000) }),
  ])
  clearTimeout(timeout)
  signalChildren('SIGKILL')
  process.exit(code)
}

process.on('SIGINT', () => void stop(130))
process.on('SIGTERM', () => void stop(143))

async function checkPort(host, port) {
  const server = createServer()
  await new Promise((resolve, reject) => {
    server.once('error', () => reject(new Error(`${host}:${port} 不可用，请检查端口占用及 .env.local。`)))
    server.listen(port, host, resolve)
  })
  await new Promise((resolve, reject) => server.close(error => error ? reject(error) : resolve()))
}

async function main() {
  if (Number(process.versions.node.split('.')[0]) < 24) throw new Error('请使用 Node.js 24 或更新版本。')
  const action = process.argv[2]
  if (action === 'setup') {
    if (!existsSync('.env.local')) {
      copyFileSync('.env.local.example', '.env.local', constants.COPYFILE_EXCL)
      chmodSync('.env.local', 0o600)
      console.log('已创建 .env.local，请配置本机 DATABASE_URL。')
    }
    await run('go', ['mod', 'download'])
    await run('go', ['install', `github.com/air-verse/air@${airVersion}`], {
      env: { ...process.env, GOBIN: resolve('bin') },
    })
    await run('npm', ['ci'], { cwd: resolve('web') })
    console.log('开发依赖已安装。配置 .env.local 后运行 make dev。')
    return
  }
  if (!['dev', 'migrate'].includes(action)) throw new Error('用法：make setup | make dev | make migrate')
  if (!existsSync('.env.local')) throw new Error('缺少 .env.local，请先运行 make setup。')
  // 外部环境变量优先，便于临时切换端口或使用独立测试数据库。
  process.loadEnvFile('.env.local')
  if (!process.env.DATABASE_URL) throw new Error('请在 .env.local 配置 DATABASE_URL。')

  if (action === 'dev') {
    if (!existsSync('bin/air') || !existsSync('web/node_modules/.bin/vite')) {
      throw new Error('缺少开发依赖，请先运行 make setup。')
    }
    if (!process.env.CYCLEDOCK_ADMIN_PASSWORD && !process.env.CYCLEDOCK_OIDC_ISSUER) {
      throw new Error('请配置 CYCLEDOCK_ADMIN_PASSWORD（至少 12 位）或 OIDC。')
    }
    if (process.env.CYCLEDOCK_ADMIN_PASSWORD && process.env.CYCLEDOCK_ADMIN_PASSWORD.length < 12) {
      throw new Error('CYCLEDOCK_ADMIN_PASSWORD 至少需要 12 位。')
    }
    process.env.HTTP_ADDR ||= '127.0.0.1:8080'
    process.env.DEV_WEB_PORT ||= '5173'
    const backend = new URL(`http://${process.env.HTTP_ADDR}`)
    const webPort = Number(process.env.DEV_WEB_PORT)
    if (!Number.isInteger(webPort) || webPort < 1 || webPort > 65535) {
      throw new Error('DEV_WEB_PORT 必须是 1–65535 的端口。')
    }
    // 先检查端口，避免发现已有服务后才对其数据库执行迁移。
    await checkPort(backend.hostname, Number(backend.port || 80))
    await checkPort('127.0.0.1', webPort)
  }

  await run('go', ['run', './cmd/migrate'])
  if (action === 'migrate') return
  console.log(`开发页面：http://127.0.0.1:${process.env.DEV_WEB_PORT}，Ctrl+C 停止前后端。`)
  await Promise.race([
    run(resolve('bin/air'), ['-c', '.air.toml'], { keepGroup: true }),
    run('npm', ['run', 'dev'], { cwd: resolve('web'), keepGroup: true }),
  ])
  throw new Error('开发进程已退出，正在停止其他进程。')
}

main().then(() => stop(0)).catch(async error => {
  console.error(error.message)
  await stop(1)
})
