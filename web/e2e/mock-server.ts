import { createServer, type IncomingMessage, type ServerResponse } from 'node:http'

const host = '127.0.0.1'
const port = 4174
const now = '2026-07-19T08:00:00.000Z'
const administratorUserId = '11111111-1111-4111-8111-111111111111'
const memberUserId = '22222222-2222-4222-8222-222222222222'
const activeModelId = '33333333-3333-4333-8333-333333333333'
const draftRevisionId = '44444444-4444-4444-8444-444444444444'
const publishedRevisionId = '55555555-5555-4555-8555-555555555555'

interface Revision {
  id: string
  sequence: number
  status: 'draft' | 'published' | 'superseded'
  createdBy: string
  createdAt: string
  publishedAt?: string
  summary: string
  validationIssueCount: number
  providerCount: number
  modelCount: number
  credentialCount: number
  routeCount: number
}

interface KeyRecord {
  id: string
  ownerId: string
  ownerName: string
  name: string
  prefix: string
  status: 'active'
  authorizedModelIds: string[]
  authorizedModels: string[]
  createdAt: string
}

interface InvitationRecord {
  id: string
  codePrefix: string
  status: 'issued' | 'claimed' | 'expired' | 'revoked'
  expiresAt: string
  createdBy: string
}

interface CreatedInvitationResult {
  invitation: InvitationRecord
  code: string
}

interface InvitationMutation {
  fingerprint: string
  result: CreatedInvitationResult
}

interface FixtureSession {
  userId: string
  displayName: string
  role: 'administrator' | 'member'
  capabilities: string[]
  csrfToken: string
  expiresAt: string
}

interface FixtureState {
  setupRequired: boolean
  authenticated: boolean
  session: FixtureSession
  invitations: InvitationRecord[]
  invitationMutations: Map<string, InvitationMutation>
  keys: KeyRecord[]
  revisions: Revision[]
  activeRevisionId: string | null
  activeVersion: number
}

const administratorSession: FixtureSession = {
  userId: administratorUserId,
  displayName: 'Gateway Admin',
  role: 'administrator',
  capabilities: [
    'providers:read',
    'providers:write',
    'credentials:read',
    'credentials:write',
    'access:read',
    'access:write',
    'ledger:read',
    'ledger:write',
    'playground:use',
    'revisions:publish',
  ],
  csrfToken: 'csrf-e2e-token',
  expiresAt: '2026-07-20T08:00:00.000Z',
}

const memberSession: FixtureSession = {
  userId: memberUserId,
  displayName: 'Gateway Member',
  role: 'member',
  capabilities: ['access:read', 'ledger:read', 'playground:use'],
  csrfToken: 'csrf-member-e2e-token',
  expiresAt: '2026-07-20T08:00:00.000Z',
}

const fixtureInvitationCode = 'invite_mock_once_complete_secret'

let state = createState()

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
      role: options.role === 'member' ? 'member' : 'administrator',
    })
    return json(response, 200, { reset: true })
  }

  if (path === '/api/control/setup/status' && request.method === 'GET') {
    return data(response, { required: state.setupRequired })
  }
  if (path === '/api/control/setup' && request.method === 'POST') {
    state.setupRequired = false
    state.authenticated = true
    state.session = administratorSession
    await readJson(request)
    return data(response, state.session)
  }
  if (path === '/api/control/session' && request.method === 'POST') {
    state.authenticated = true
    await readJson(request)
    return data(response, state.session)
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
    return data(response, state.session)
  }

  if (path === '/api/control/provider-kinds' && request.method === 'GET') {
    return data(response, [
      { kind: 'agnes', displayName: 'Agnes' },
      { kind: 'gemini', displayName: 'Google Gemini' },
      { kind: 'openai-compatible', displayName: 'OpenAI-compatible' },
      { kind: 'zhipu', displayName: '智谱 GLM' },
    ])
  }
  if (path === '/api/control/providers' && request.method === 'GET') {
    return data(response, page([provider]))
  }
  if (path === '/api/control/credentials' && request.method === 'GET') {
    return data(response, page([]))
  }
  if (path === '/api/control/users' && request.method === 'GET') {
    return data(response, page([user]))
  }
  if (path === '/api/control/invitations' && request.method === 'GET') {
    return data(response, page(state.invitations))
  }
  if (path === '/api/control/invitations' && request.method === 'POST') {
    const input = await readJson(request)
    const idempotencyKey = headerValue(request, 'idempotency-key')
    if (!idempotencyKey) {
      return problem(response, 400, 'invalid_request', 'Idempotency-Key is required.')
    }
    const expiresAt = typeof input.expiresAt === 'string' ? input.expiresAt : now
    const fingerprint = JSON.stringify({ expiresAt })
    const mutationKey = `${state.session.userId}:${idempotencyKey}`
    const existing = state.invitationMutations.get(mutationKey)
    if (existing) {
      if (existing.fingerprint !== fingerprint) {
        return problem(
          response,
          409,
          'idempotency_conflict',
          'Idempotency-Key was already used for different invitation input.',
        )
      }
      response.setHeader('Cache-Control', 'no-store')
      return data(response, existing.result, 201)
    }
    const invitation: InvitationRecord = {
      id: `invitation-${state.invitations.length + 1}`,
      codePrefix: fixtureInvitationCode.slice(0, 13),
      status: 'issued',
      expiresAt,
      createdBy: state.session.displayName,
    }
    const result = { invitation, code: fixtureInvitationCode }
    state.invitations = [invitation, ...state.invitations]
    state.invitationMutations.set(mutationKey, { fingerprint, result })
    response.setHeader('Cache-Control', 'no-store')
    return data(response, result, 201)
  }
  const invitationRevokeMatch = path.match(/^\/api\/control\/invitations\/([^/]+)\/revoke$/)
  if (invitationRevokeMatch && request.method === 'POST') {
    const invitationID = decodeURIComponent(invitationRevokeMatch[1] ?? '')
    let revoked: InvitationRecord | undefined
    state.invitations = state.invitations.map((invitation) => {
      if (invitation.id !== invitationID) return invitation
      revoked = { ...invitation, status: 'revoked' }
      return revoked
    })
    return revoked
      ? data(response, revoked)
      : problem(response, 404, 'not_found', 'Invitation was not found.')
  }
  if (path === '/api/control/keys' && request.method === 'GET') {
    return data(response, page(state.keys))
  }
  if (path === '/api/control/keys' && request.method === 'POST') {
    const input = await readJson(request)
    const authorizedModelIds = Array.isArray(input.authorizedModelIds)
      ? input.authorizedModelIds.map(String)
      : []
    const key: KeyRecord = {
      id: `key-${state.keys.length + 1}`,
      ownerId: String(input.ownerId),
      ownerName: 'Gateway Admin',
      name: String(input.name),
      prefix: 'llmg_7F2A_on',
      status: 'active',
      authorizedModelIds,
      authorizedModels: authorizedModelIds.includes(activeModelId) ? ['gpt-main'] : [],
      createdAt: now,
    }
    state.keys = [key, ...state.keys]
    return data(response, { key, secret: 'test-only' }, 201)
  }

  if (path === '/api/control/configuration/revisions' && request.method === 'GET') {
    return data(response, page(state.revisions))
  }
  if (path === '/api/control/configuration/active' && request.method === 'GET') {
    const activeRevision = state.revisions.find(
      (revision) => revision.id === state.activeRevisionId,
    )
    return data(response, {
      revisionId: state.activeRevisionId,
      sequence: activeRevision?.sequence ?? 0,
      version: state.activeVersion,
      updatedAt: state.activeRevisionId ? now : null,
      models: state.activeRevisionId
        ? [
            {
              id: activeModelId,
              alias: 'gpt-main',
              displayName: 'gpt-main',
              providerId: 'provider-main',
              providerName: 'Primary Provider',
              resourceDomain: 'professional',
            },
          ]
        : [],
    })
  }
  if (path === '/api/control/configuration/revisions' && request.method === 'POST') {
    const nextSequence = Math.max(...state.revisions.map((revision) => revision.sequence)) + 1
    const revision: Revision = {
      id: '66666666-6666-4666-8666-' + String(nextSequence).padStart(12, '0'),
      sequence: nextSequence,
      status: 'draft',
      createdBy: state.session.displayName,
      createdAt: now,
      summary: '1 Provider / 1 模型 / 1 凭据',
      validationIssueCount: 0,
      providerCount: 1,
      modelCount: 1,
      credentialCount: 1,
      routeCount: 1,
    }
    state.revisions = [revision, ...state.revisions]
    return data(response, revision, 201)
  }
  const publicationMatch = path.match(
    /^\/api\/control\/configuration\/revisions\/([^/]+)\/(publish|rollback)$/,
  )
  if (publicationMatch && request.method === 'POST') {
    const input = await readJson(request)
    if (Number(input.expectedActiveVersion) !== state.activeVersion) {
      return problem(response, 409, 'configuration_conflict', 'The active configuration changed.')
    }
    const revisionId = decodeURIComponent(publicationMatch[1] ?? '')
    const action = publicationMatch[2] ?? 'publish'
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
    state.activeRevisionId = revisionId
    state.activeVersion += 1
    return data(
      response,
      operation(
        'op-' + action,
        action === 'rollback' ? 'Configuration rollback completed.' : 'Configuration published.',
      ),
    )
  }
  if (path === '/api/control/playground/models' && request.method === 'GET') {
    if (!url.searchParams.get('gatewayKeyId')) {
      return problem(response, 400, 'invalid_gateway_key', 'Select an active gateway Key.')
    }
    return data(response, [
      {
        id: 'model-main',
        alias: 'gpt-main',
        providerName: 'Primary Provider',
        capabilities: ['streaming', 'tools', 'reasoning'],
        reasoningMode: 'hybrid',
      },
    ])
  }
  if (path === '/api/control/playground/runs' && request.method === 'POST') {
    const input = await readJson(request)
    if (!headerValue(request, 'idempotency-key')) {
      return problem(response, 400, 'invalid_idempotency_key', 'Idempotency-Key is required.')
    }
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
  options: {
    setupRequired?: boolean
    authenticated?: boolean
    role?: FixtureSession['role']
  } = {},
): FixtureState {
  return {
    setupRequired: options.setupRequired ?? false,
    authenticated: options.authenticated ?? true,
    session: options.role === 'member' ? memberSession : administratorSession,
    invitations: [],
    invitationMutations: new Map(),
    keys: [
      {
        id: 'key-playground',
        ownerId: options.role === 'member' ? memberUserId : administratorUserId,
        ownerName: options.role === 'member' ? 'Gateway Member' : 'Gateway Admin',
        name: 'Playground Key',
        prefix: 'llmg_playground',
        status: 'active',
        authorizedModelIds: [activeModelId],
        authorizedModels: ['gpt-main'],
        createdAt: now,
      },
    ],
    activeRevisionId: publishedRevisionId,
    activeVersion: 7,
    revisions: [
      {
        id: draftRevisionId,
        sequence: 42,
        status: 'draft',
        createdBy: 'Gateway Admin',
        createdAt: now,
        summary: 'Add primary route',
        validationIssueCount: 0,
        providerCount: 1,
        modelCount: 1,
        credentialCount: 1,
        routeCount: 1,
      },
      {
        id: publishedRevisionId,
        sequence: 41,
        status: 'published',
        createdBy: 'Gateway Admin',
        createdAt: '2026-07-18T08:00:00.000Z',
        publishedAt: '2026-07-18T08:05:00.000Z',
        summary: 'Current production configuration',
        validationIssueCount: 0,
        providerCount: 1,
        modelCount: 1,
        credentialCount: 1,
        routeCount: 1,
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

function headerValue(request: IncomingMessage, name: string): string {
  const value = request.headers[name]
  return Array.isArray(value) ? (value[0] ?? '') : (value ?? '')
}

function delay(milliseconds: number) {
  return new Promise<void>((resolve) => setTimeout(resolve, milliseconds))
}

const user = {
  id: administratorUserId,
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
