import { createServer, type IncomingMessage, type ServerResponse } from 'node:http'

const host = '127.0.0.1'
const port = 4174
const now = '2026-07-19T08:00:00.000Z'

interface Revision {
  id: string
  sequence: number
  status: 'draft' | 'published' | 'superseded'
  createdBy: string
  createdAt: string
  publishedAt?: string
  summary: string
  validationIssueCount: number
}

interface KeyRecord {
  id: string
  ownerId: string
  ownerName: string
  name: string
  prefix: string
  status: 'active'
  authorizedModels: string[]
  createdAt: string
}

interface FixtureState {
  setupRequired: boolean
  authenticated: boolean
  keys: KeyRecord[]
  revisions: Revision[]
}

let state = createState()

const session = {
  userId: 'user-admin',
  displayName: 'Gateway Admin',
  role: 'administrator',
  capabilities: [
    'overview:read',
    'providers:read',
    'providers:write',
    'credentials:read',
    'credentials:write',
    'access:read',
    'access:write',
    'ledger:read',
    'ledger:write',
    'operations:read',
    'audit:read',
    'content:read',
    'playground:use',
    'settings:read',
    'settings:write',
    'revisions:publish',
  ],
  csrfToken: 'csrf-e2e-token',
  expiresAt: '2026-07-20T08:00:00.000Z',
}

const server = createServer((request, response) => {
  void handleRequest(request, response)
})

async function handleRequest(request: IncomingMessage, response: ServerResponse) {
  const url = new URL(request.url ?? '/', `http://${host}:${port}`)
  const path = url.pathname

  if (path === '/__test/health') return json(response, 200, { ok: true })
  if (path === '/__test/reset' && request.method === 'POST') {
    const options = await readJson(request)
    state = createState({
      setupRequired: options.setupRequired === true,
      authenticated: options.authenticated !== false,
    })
    return json(response, 200, { reset: true })
  }

  if (path === '/api/control/setup/status' && request.method === 'GET') {
    return data(response, { required: state.setupRequired })
  }
  if (path === '/api/control/setup' && request.method === 'POST') {
    state.setupRequired = false
    state.authenticated = true
    await readJson(request)
    return data(response, session)
  }
  if (path === '/api/control/session' && request.method === 'POST') {
    state.authenticated = true
    await readJson(request)
    return data(response, session)
  }
  if (path === '/api/control/session' && request.method === 'DELETE') {
    state.authenticated = false
    response.writeHead(204)
    return response.end()
  }
  if (path === '/api/control/session' && request.method === 'GET') {
    if (!state.authenticated) {
      return problem(response, 401, 'session_required', '请先登录')
    }
    return data(response, session)
  }

  if (path === '/api/control/overview' && request.method === 'GET') {
    return data(response, overview)
  }
  if (path === '/api/control/providers' && request.method === 'GET') {
    return data(response, page([provider]))
  }
  if (path === '/api/control/users' && request.method === 'GET') {
    return data(response, page([user]))
  }
  if (path === '/api/control/keys' && request.method === 'GET') {
    return data(response, page(state.keys))
  }
  if (path === '/api/control/keys' && request.method === 'POST') {
    const input = await readJson(request)
    const key: KeyRecord = {
      id: `key-${state.keys.length + 1}`,
      ownerId: String(input.ownerId),
      ownerName: 'Gateway Admin',
      name: String(input.name),
      prefix: 'lgw_live_7F2A',
      status: 'active',
      authorizedModels: Array.isArray(input.authorizedModels)
        ? input.authorizedModels.map(String)
        : [],
      createdAt: now,
    }
    state.keys = [key, ...state.keys]
    return data(response, { key, secret: 'lgw_live_7F2A_once_secret' }, 201)
  }

  if (path === '/api/control/configuration/revisions' && request.method === 'GET') {
    return data(response, page(state.revisions))
  }
  const publishMatch = path.match(/^\/api\/control\/configuration\/revisions\/([^/]+)\/publish$/)
  if (publishMatch && request.method === 'POST') {
    await readJson(request)
    const revisionId = decodeURIComponent(publishMatch[1] ?? '')
    state.revisions = state.revisions.map((revision) => ({
      ...revision,
      status:
        revision.id === revisionId
          ? 'published'
          : revision.status === 'published'
            ? 'superseded'
            : revision.status,
      ...(revision.id === revisionId ? { publishedAt: now } : {}),
    }))
    return data(response, operation('op-publish', '配置已发布'))
  }
  if (path === '/api/control/operations/op-publish' && request.method === 'GET') {
    return data(response, operation('op-publish', '配置已发布'))
  }

  if (path === '/api/control/playground/models' && request.method === 'GET') {
    return data(response, [
      {
        id: 'model-main',
        alias: 'gpt-main',
        providerName: 'Primary Provider',
        capabilities: ['streaming', 'tools', 'reasoning'],
      },
    ])
  }
  if (path === '/api/control/playground/runs' && request.method === 'POST') {
    const input = await readJson(request)
    const messages = toUnknownArray(input.messages)
    const last = messages.at(-1)
    const prompt = isRecord(last) && typeof last.content === 'string' ? last.content : ''
    if (prompt.includes('触发错误')) {
      return problem(response, 429, 'provider_busy', '上游当前繁忙', true, 'dispatching')
    }
    return playgroundStream(request, response, prompt.includes('等待取消'))
  }

  return problem(response, 404, 'fixture_route_missing', `未配置测试接口：${path}`)
}

server.listen(port, host, () => {
  process.stdout.write(`E2E API fixture listening on http://${host}:${port}\n`)
})

function createState(
  options: { setupRequired?: boolean; authenticated?: boolean } = {},
): FixtureState {
  return {
    setupRequired: options.setupRequired ?? false,
    authenticated: options.authenticated ?? true,
    keys: [],
    revisions: [
      {
        id: 'revision-42',
        sequence: 42,
        status: 'draft',
        createdBy: 'Gateway Admin',
        createdAt: now,
        summary: 'Add primary route',
        validationIssueCount: 0,
      },
      {
        id: 'revision-41',
        sequence: 41,
        status: 'published',
        createdBy: 'Gateway Admin',
        createdAt: '2026-07-18T08:00:00.000Z',
        publishedAt: '2026-07-18T08:05:00.000Z',
        summary: 'Current production configuration',
        validationIssueCount: 0,
      },
    ],
  }
}

async function playgroundStream(
  _request: IncomingMessage,
  response: ServerResponse,
  holdOpen: boolean,
) {
  response.writeHead(200, {
    'Content-Type': 'text/event-stream',
    'Cache-Control': 'no-cache, no-transform',
    Connection: 'keep-alive',
    'X-Accel-Buffering': 'no',
  })
  response.flushHeaders()
  sendEvent(response, {
    type: 'phase',
    phase: 'streaming',
    step: holdOpen ? '等待上游响应' : '接收上游响应',
    requestId: holdOpen ? 'req-cancel' : 'req-stream',
  })
  if (holdOpen) {
    const heartbeat = setInterval(() => response.write(': keepalive\n\n'), 250)
    response.on('close', () => clearInterval(heartbeat))
    return
  }
  const events = [
    { type: 'reasoning', delta: '先核对当前事实。' },
    { type: 'content', delta: '这是' },
    { type: 'content', delta: '流式响应' },
    { type: 'usage', inputTokens: 12, outputTokens: 6, source: 'authoritative' },
    { type: 'completed', requestId: 'req-stream' },
  ]
  for (const event of events) {
    await delay(20)
    if (response.destroyed) return
    sendEvent(response, event)
  }
  response.end()
}

function sendEvent(response: ServerResponse, event: object) {
  response.write(`data: ${JSON.stringify(event)}\n\n`)
}

function operation(id: string, step: string) {
  return {
    id,
    kind: 'configuration.publish',
    phase: 'completed',
    step,
    progress: 100,
    requestId: 'req-publish',
    createdAt: now,
    updatedAt: now,
    canCancel: false,
  }
}

function page<T>(items: T[]) {
  return { items, page: 1, pageSize: 20, total: items.length }
}

function data(response: ServerResponse, value: unknown, status = 200) {
  return json(response, status, { data: value })
}

function problem(
  response: ServerResponse,
  status: number,
  code: string,
  message: string,
  retryable = false,
  stage?: string,
) {
  return json(response, status, {
    error: {
      status,
      code,
      message,
      retryable,
      requestId: `fixture-${status}`,
      ...(stage ? { stage } : {}),
    },
  })
}

function json(response: ServerResponse, status: number, value: unknown) {
  response.writeHead(status, { 'Content-Type': 'application/json; charset=utf-8' })
  response.end(JSON.stringify(value))
}

async function readJson(request: IncomingMessage): Promise<Record<string, unknown>> {
  const chunks: string[] = []
  for await (const chunk of request as AsyncIterable<unknown>) {
    if (typeof chunk === 'string') chunks.push(chunk)
    else if (Buffer.isBuffer(chunk)) chunks.push(chunk.toString('utf8'))
  }
  if (chunks.length === 0) return {}
  const parsed: unknown = JSON.parse(chunks.join(''))
  return isRecord(parsed) ? parsed : {}
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
}

function toUnknownArray(value: unknown): unknown[] {
  return Array.isArray(value) ? (value as unknown[]) : []
}

function delay(milliseconds: number) {
  return new Promise<void>((resolve) => setTimeout(resolve, milliseconds))
}

const user = {
  id: 'user-admin',
  displayName: 'Gateway Admin',
  email: 'admin@example.com',
  role: 'administrator',
  status: 'active',
  modelCount: 1,
  keyCount: 0,
  quotaRemainingTokens: 2_000_000,
  createdAt: now,
  lastActiveAt: now,
}

const provider = {
  id: 'provider-main',
  slug: 'primary-provider',
  name: 'Primary Provider',
  kind: 'openai-compatible',
  baseUrl: 'https://provider.example/v1',
  status: 'enabled',
  modelCount: 1,
  credentialCount: 2,
  verifiedAt: now,
  updatedAt: now,
}

const overview = {
  health: 'healthy',
  requests24h: 1280,
  successRate: 0.997,
  p95LatencyMs: 820,
  queuedRequests: 2,
  series: [
    {
      timestamp: now,
      requests: 52,
      inputTokens: 3200,
      outputTokens: 1200,
      errorRate: 0.003,
    },
  ],
  pools: [
    {
      resourceDomain: 'professional',
      readyCredentials: 2,
      busyCredentials: 1,
      coolingCredentials: 0,
      queuedRequests: 2,
    },
  ],
  alerts: [],
}
