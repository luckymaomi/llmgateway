import { afterAll, afterEach, beforeAll } from 'vitest'

import { server } from './server'

const browserFetch = globalThis.fetch

beforeAll(() => {
  server.listen({ onUnhandledRequest: 'error' })
  const interceptedFetch = globalThis.fetch
  globalThis.fetch = (input: RequestInfo | URL, init?: RequestInit) => {
    const request =
      typeof input === 'string' && input.startsWith('/')
        ? new URL(input, 'http://llmgateway.test')
        : input
    return interceptedFetch(request, init)
  }
})
afterEach(() => server.resetHandlers())
afterAll(() => {
  server.close()
  globalThis.fetch = browserFetch
})
