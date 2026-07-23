import type { ApiProblemShape, ListQuery } from './types'

type QueryValue = string | number | boolean | null | undefined
type Query = Record<string, QueryValue | QueryValue[]>

export class ApiProblem extends Error implements ApiProblemShape {
  readonly status: number
  readonly code: string
  readonly stage?: string
  readonly requestId?: string
  readonly retryable: boolean
  readonly fieldErrors?: Record<string, string>

  constructor(problem: ApiProblemShape) {
    super(problem.message)
    this.name = 'ApiProblem'
    this.status = problem.status
    this.code = problem.code
    this.retryable = problem.retryable
    if (problem.stage !== undefined) this.stage = problem.stage
    if (problem.requestId !== undefined) this.requestId = problem.requestId
    if (problem.fieldErrors !== undefined) this.fieldErrors = problem.fieldErrors
  }
}

interface RequestOptions<TBody> {
  method?: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE'
  query?: Query
  body?: TBody
  signal?: AbortSignal
  headers?: Record<string, string>
}

interface ApiEnvelope<T> {
  data: T
}

function withQuery(path: string, query?: Query): string {
  if (!query) return path
  const search = new URLSearchParams()
  for (const [key, value] of Object.entries(query)) {
    if (value === undefined || value === null || value === '') continue
    if (Array.isArray(value)) {
      for (const item of value) {
        if (item !== undefined && item !== null && item !== '') search.append(key, String(item))
      }
    } else {
      search.set(key, String(value))
    }
  }
  const encoded = search.toString()
  return encoded ? `${path}?${encoded}` : path
}

function isEnvelope<T>(value: unknown): value is ApiEnvelope<T> {
  return typeof value === 'object' && value !== null && 'data' in value
}

function asProblem(status: number, body: unknown, requestIdHeader: string | null): ApiProblem {
  const record = typeof body === 'object' && body !== null ? (body as Record<string, unknown>) : {}
  const nested =
    typeof record.error === 'object' && record.error !== null
      ? (record.error as Record<string, unknown>)
      : record
  const fieldErrors =
    typeof nested.fieldErrors === 'object' && nested.fieldErrors !== null
      ? (nested.fieldErrors as Record<string, string>)
      : undefined
  return new ApiProblem({
    status,
    code: typeof nested.code === 'string' ? nested.code : `http_${status}`,
    message:
      typeof nested.message === 'string'
        ? nested.message
        : typeof body === 'string' && body
          ? body
          : '请求未完成',
    retryable: nested.retryable === true,
    ...(typeof nested.stage === 'string' ? { stage: nested.stage } : {}),
    ...(typeof nested.requestId === 'string'
      ? { requestId: nested.requestId }
      : requestIdHeader
        ? { requestId: requestIdHeader }
        : {}),
    ...(fieldErrors ? { fieldErrors } : {}),
  })
}

class SameOriginApiClient {
  private csrfToken = ''

  setCsrfToken(token: string): void {
    this.csrfToken = token
  }

  async request<TResponse, TBody = unknown>(
    path: string,
    options: RequestOptions<TBody> = {},
  ): Promise<TResponse> {
    const method = options.method ?? 'GET'
    const headers: Record<string, string> = {
      Accept: 'application/json',
      ...options.headers,
    }
    if (options.body !== undefined) headers['Content-Type'] = 'application/json'
    if (this.csrfToken && method !== 'GET') headers['X-CSRF-Token'] = this.csrfToken

    let response: Response
    try {
      response = await fetch(withQuery(path, options.query), {
        method,
        credentials: 'include',
        headers,
        ...(options.body !== undefined ? { body: JSON.stringify(options.body) } : {}),
        ...(options.signal ? { signal: options.signal } : {}),
      })
    } catch (error) {
      if (error instanceof DOMException && error.name === 'AbortError') throw error
      throw new ApiProblem({
        status: 0,
        code: 'network_unavailable',
        message: '无法连接 LLMGateway',
        retryable: true,
      })
    }

    const contentType = response.headers.get('content-type') ?? ''
    const body: unknown = contentType.includes('application/json')
      ? await response.json()
      : await response.text()

    if (!response.ok) {
      throw asProblem(response.status, body, response.headers.get('x-request-id'))
    }

    return (isEnvelope<TResponse>(body) ? body.data : body) as TResponse
  }

  async stream<TBody>(
    path: string,
    body: TBody,
    signal: AbortSignal,
    extraHeaders: Record<string, string> = {},
  ): Promise<Response> {
    const headers: Record<string, string> = {
      Accept: 'text/event-stream',
      'Content-Type': 'application/json',
      ...extraHeaders,
    }
    if (this.csrfToken) headers['X-CSRF-Token'] = this.csrfToken
    const response = await fetch(path, {
      method: 'POST',
      credentials: 'include',
      headers,
      body: JSON.stringify(body),
      signal,
    })
    if (!response.ok) {
      const contentType = response.headers.get('content-type') ?? ''
      const responseBody: unknown = contentType.includes('application/json')
        ? await response.json()
        : await response.text()
      throw asProblem(response.status, responseBody, response.headers.get('x-request-id'))
    }
    return response
  }
}

export const apiClient = new SameOriginApiClient()

export function listQuery(query: ListQuery): Query {
  return { ...query }
}
