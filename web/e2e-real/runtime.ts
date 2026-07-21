import { spawn, type ChildProcess } from 'node:child_process'
import { closeSync, existsSync, mkdirSync, openSync, rmSync, writeFileSync } from 'node:fs'
import path from 'node:path'
import { setTimeout as delay } from 'node:timers/promises'

import { expect, test as base, type Page, type Response } from '@playwright/test'

export interface GatewayRuntime {
  restart(): Promise<void>
}

interface TestFixtures {
  browserProblems: BrowserProblems
}

interface WorkerFixtures {
  gateway: GatewayRuntime
}

class GatewayProcess implements GatewayRuntime {
  private child: ChildProcess | undefined
  private generation = 0
  private launchError: Error | undefined
  private readonly binary = requiredEnvironment('LLMGATEWAY_REAL_GATEWAY_BINARY')
  private readonly gatewayURL = requiredEnvironment('LLMGATEWAY_REAL_GATEWAY_URL')
  private readonly logDirectory = requiredEnvironment('LLMGATEWAY_REAL_GATEWAY_LOG_DIR')
  private readonly pidFile = requiredEnvironment('LLMGATEWAY_REAL_GATEWAY_PID_FILE')

  async start(): Promise<void> {
    if (this.child && this.child.exitCode === null && this.child.signalCode === null) {
      throw new Error('gateway is already running')
    }
    if (!existsSync(this.binary)) throw new Error('gateway test binary does not exist')

    mkdirSync(this.logDirectory, { recursive: true })
    this.generation += 1
    this.launchError = undefined
    const stdoutPath = path.join(this.logDirectory, `gateway-${this.generation}.stdout.log`)
    const stderrPath = path.join(this.logDirectory, `gateway-${this.generation}.stderr.log`)
    const stdout = openSync(stdoutPath, 'a')
    const stderr = openSync(stderrPath, 'a')
    try {
      this.child = spawn(this.binary, [], {
        env: gatewayEnvironment(),
        stdio: ['ignore', stdout, stderr],
        windowsHide: true,
      })
    } finally {
      closeSync(stdout)
      closeSync(stderr)
    }
    this.child.once('error', (error) => {
      this.launchError = error
    })
    try {
      if (!this.child.pid) throw new Error('gateway process did not expose a PID')
      writeFileSync(this.pidFile, String(this.child.pid), { encoding: 'utf8', mode: 0o600 })
      await this.waitUntilReady()
    } catch (error) {
      await this.stop(true)
      throw error
    }
  }

  async restart(): Promise<void> {
    await this.stop(true)
    await this.start()
  }

  async stop(force = false): Promise<void> {
    const child = this.child
    this.child = undefined
    if (!child || child.exitCode !== null || child.signalCode !== null) {
      rmSync(this.pidFile, { force: true })
      return
    }

    child.kill(force ? 'SIGKILL' : 'SIGTERM')
    if (await waitForExit(child, 5_000)) {
      rmSync(this.pidFile, { force: true })
      return
    }
    child.kill('SIGKILL')
    if (!(await waitForExit(child, 5_000))) {
      throw new Error(`gateway process ${child.pid ?? 'unknown'} did not exit`)
    }
    rmSync(this.pidFile, { force: true })
  }

  private async waitUntilReady(): Promise<void> {
    const deadline = Date.now() + 30_000
    while (Date.now() < deadline) {
      if (this.launchError)
        throw new Error('gateway process could not start', { cause: this.launchError })
      if (!this.child || this.child.exitCode !== null || this.child.signalCode !== null) {
        throw new Error(`gateway exited before readiness; inspect ${this.logDirectory}`)
      }
      try {
        const response = await fetch(`${this.gatewayURL}/health/ready`, {
          signal: AbortSignal.timeout(1_000),
        })
        if (response.ok) return
      } catch {
        // The listener and storage dependencies become observable through readiness.
      }
      await delay(100)
    }
    throw new Error(`gateway did not become ready; inspect ${this.logDirectory}`)
  }
}

export class BrowserProblems {
  private readonly problems: string[] = []
  private readonly allowances = new Map<string, number>()

  observe(page: Page): void {
    page.on('console', (message) => {
      if (message.type() !== 'error') return
      if (message.text().startsWith('Failed to load resource:')) return
      this.problems.push(`console: ${message.text()}`)
    })
    page.on('pageerror', (error) => this.problems.push(`pageerror: ${error.message}`))
    page.on('requestfailed', (request) => {
      const failure = request.failure()
      if (request.method() === 'GET' && failure?.errorText === 'net::ERR_ABORTED') return
      const url = new URL(request.url())
      this.problems.push(
        formatRequestFailure(request.method(), url.pathname, failure?.errorText ?? 'unknown'),
      )
    })
    page.on('response', (response) => {
      if (response.status() >= 400) this.problems.push(formatResponseProblem(response))
    })
  }

  allow(response: Response): void {
    const problem = formatResponseProblem(response)
    this.allowances.set(problem, (this.allowances.get(problem) ?? 0) + 1)
  }

  allowRequestFailure(method: string, pathname: string, errorText: string): void {
    const problem = formatRequestFailure(method, pathname, errorText)
    this.allowances.set(problem, (this.allowances.get(problem) ?? 0) + 1)
  }

  assertClean(): void {
    const remaining = new Map(this.allowances)
    const unexpected = this.problems.filter((problem) => {
      const count = remaining.get(problem) ?? 0
      if (count === 0) return true
      remaining.set(problem, count - 1)
      return false
    })
    const unused = [...remaining.entries()].filter(([, count]) => count > 0)
    expect({ unexpected, unused }, 'browser console, page, and HTTP errors').toEqual({
      unexpected: [],
      unused: [],
    })
  }
}

export const test = base.extend<TestFixtures, WorkerFixtures>({
  gateway: [
    async ({ browserName }, provide) => {
      void browserName
      const gateway = new GatewayProcess()
      try {
        await gateway.start()
        await provide(gateway)
      } finally {
        await gateway.stop()
      }
    },
    { scope: 'worker' },
  ],
  browserProblems: [
    async ({ page }, use) => {
      const problems = new BrowserProblems()
      problems.observe(page)
      await use(problems)
      problems.assertClean()
    },
    { auto: true },
  ],
})

export function acceptanceArtifactPath(fileName: string): string {
  if (path.basename(fileName) !== fileName)
    throw new Error('acceptance artifact must be a file name')
  return path.join(requiredEnvironment('LLMGATEWAY_REAL_GATEWAY_LOG_DIR'), fileName)
}

function formatResponseProblem(response: Response): string {
  const url = new URL(response.url())
  return `http: ${response.request().method()} ${url.pathname} -> ${response.status()}`
}

function formatRequestFailure(method: string, pathname: string, errorText: string): string {
  return `requestfailed: ${method} ${pathname} -> ${errorText}`
}

async function waitForExit(child: ChildProcess, timeoutMilliseconds: number): Promise<boolean> {
  if (child.exitCode !== null || child.signalCode !== null) return true
  return await new Promise((resolve) => {
    const timer = setTimeout(() => {
      child.removeListener('exit', onExit)
      resolve(false)
    }, timeoutMilliseconds)
    const onExit = () => {
      clearTimeout(timer)
      resolve(true)
    }
    child.once('exit', onExit)
  })
}

export function requiredEnvironment(name: string): string {
  const value = process.env[name]
  if (!value) throw new Error(`${name} is required`)
  return value
}

function gatewayEnvironment(): NodeJS.ProcessEnv {
  const environment: NodeJS.ProcessEnv = {}
  for (const name of [
    'PATH',
    'Path',
    'SystemRoot',
    'WINDIR',
    'HOME',
    'USERPROFILE',
    'TEMP',
    'TMP',
    'TZ',
    'LLMGATEWAY_PROFILE',
    'LLMGATEWAY_HTTP_ADDRESS',
    'LLMGATEWAY_DATABASE_URL',
    'LLMGATEWAY_DATABASE_MIGRATE_ON_START',
    'LLMGATEWAY_VALKEY_ADDRESS',
    'LLMGATEWAY_VALKEY_PASSWORD',
    'LLMGATEWAY_VALKEY_DATABASE',
    'LLMGATEWAY_MASTER_KEYS',
    'LLMGATEWAY_ACTIVE_MASTER_KEY_VERSION',
    'LLMGATEWAY_SESSION_PEPPER',
    'LLMGATEWAY_API_KEY_PEPPER',
    'LLMGATEWAY_COOKIE_SECURE',
    'LLMGATEWAY_ALLOWED_RESOLVED_NETWORKS',
    'LLMGATEWAY_PROVIDER_CA_BUNDLE_FILE',
    'LLMGATEWAY_LOG_LEVEL',
  ]) {
    const value = process.env[name]
    if (value !== undefined) environment[name] = value
  }
  return environment
}
