import { describe, expect, it } from 'vitest'

import type { Capability, Role, Session } from '@/api'

import { defaultRouteFor, navigationFor } from './navigation'

const cases: Array<{
  role: Role
  capabilities: Capability[]
  navigation: Array<{ label: string; to: string }>
  defaultRoute: string
}> = [
  {
    role: 'administrator',
    capabilities: [
      'providers:read',
      'providers:write',
      'access:read',
      'access:write',
      'revisions:publish',
    ],
    navigation: [
      { label: 'Provider 与模型', to: '/providers/providers' },
      { label: '用户与网关 Key', to: '/access/users' },
    ],
    defaultRoute: '/providers/providers',
  },
  {
    role: 'member',
    capabilities: ['access:read', 'ledger:read'],
    navigation: [
      { label: '我的网关 Key', to: '/access/keys' },
      { label: '我的用量', to: '/ledger/usage' },
    ],
    defaultRoute: '/access/keys',
  },
]

describe('production navigation contract', () => {
  for (const testCase of cases) {
    it(`maps ${testCase.role} to wired control surfaces`, () => {
      const session: Session = {
        userId: `${testCase.role}-id`,
        displayName: testCase.role,
        role: testCase.role,
        capabilities: testCase.capabilities,
        csrfToken: 'fixture-csrf',
        expiresAt: '2026-07-20T12:00:00Z',
      }

      expect(navigationFor(session).map(({ label, to }) => ({ label, to }))).toEqual(
        testCase.navigation,
      )
      expect(defaultRouteFor(session)).toBe(testCase.defaultRoute)
    })
  }
})
