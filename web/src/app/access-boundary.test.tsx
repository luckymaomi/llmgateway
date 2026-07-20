import { QueryClientProvider } from '@tanstack/react-query'
import { RouterProvider } from '@tanstack/react-router'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import type { GatewayKey, Invitation, Page, Session, UserAccount } from '@/api'
import { server } from '@/test/server'

import { createQueryClient } from './query-client'
import { createAppRouter } from './router'
import { establishAuthenticatedSession } from './session'

const listState = { page: 1, pageSize: 20, search: '', status: '' }

describe('authenticated access boundary', () => {
  it('clears administrator facts before a member session can render or request a management route', async () => {
    const user = userEvent.setup()
    const queryClient = createQueryClient()
    establishAuthenticatedSession(queryClient, administratorSession)
    queryClient.setQueryData<Page<UserAccount>>(['users', listState], {
      items: [privateAdministratorUser],
      page: 1,
      pageSize: 20,
      total: 1,
    })
    queryClient.setQueryData<Page<GatewayKey>>(['gateway-keys', listState], {
      items: [administratorKey],
      page: 1,
      pageSize: 20,
      total: 1,
    })

    let activeSession: Session | undefined = administratorSession
    let managementUserRequests = 0
    let managementInvitationRequests = 0
    server.use(
      http.get('/api/control/session', () =>
        activeSession
          ? HttpResponse.json({ data: activeSession })
          : HttpResponse.json(
              { error: { status: 401, code: 'session_required', message: 'Login required.' } },
              { status: 401 },
            ),
      ),
      http.delete('/api/control/session', () => {
        activeSession = undefined
        return new HttpResponse(null, { status: 204 })
      }),
      http.post('/api/control/session', () => {
        activeSession = memberSession
        return HttpResponse.json({ data: memberSession })
      }),
      http.get('/api/control/keys', () =>
        HttpResponse.json({
          data: { items: [memberKey], page: 1, pageSize: 20, total: 1 },
        }),
      ),
      http.get('/api/control/users', () => {
        managementUserRequests += 1
        return HttpResponse.json({
          data: { items: [privateAdministratorUser], page: 1, pageSize: 20, total: 1 },
        })
      }),
      http.get('/api/control/invitations', () => {
        managementInvitationRequests += 1
        return HttpResponse.json({
          data: { items: [issuedInvitation], page: 1, pageSize: 20, total: 1 },
        })
      }),
    )

    const router = createAppRouter(queryClient, ['/access/users'])
    render(
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={router} />
      </QueryClientProvider>,
    )

    expect((await screen.findAllByText(privateAdministratorUser.email)).length).toBeGreaterThan(0)
    await user.click(screen.getByRole('button', { name: '退出登录' }))
    await screen.findByRole('heading', { name: '登录' })
    await user.type(screen.getByLabelText('邮箱'), 'member@example.test')
    await user.type(screen.getByLabelText('密码'), 'member-password')
    await user.click(screen.getByRole('button', { name: '登录' }))

    expect(await screen.findByRole('heading', { name: '我的网关 Key' })).toBeVisible()
    expect((await screen.findAllByText(memberKey.name)).length).toBeGreaterThan(0)
    expect(screen.queryAllByText(administratorKey.name)).toHaveLength(0)
    expect(queryClient.getQueriesData({ queryKey: ['users'] })).toEqual([])

    await router.navigate({ to: '/access/users' })
    expect(await screen.findByRole('heading', { name: '当前会话无权执行此任务' })).toBeVisible()
    expect(screen.queryAllByText(privateAdministratorUser.email)).toHaveLength(0)
    expect(managementUserRequests).toBe(0)

    await router.navigate({ to: '/access/invitations' })
    expect(await screen.findByRole('heading', { name: '当前会话无权执行此任务' })).toBeVisible()
    expect(managementInvitationRequests).toBe(0)
  })

  it.each([
    { name: 'read-only', capabilities: ['access:read'] as Session['capabilities'], visible: false },
    {
      name: 'read-write',
      capabilities: ['access:read', 'access:write'] as Session['capabilities'],
      visible: true,
    },
  ])(
    'shows invitation mutations only for $name administrator access',
    async ({ capabilities, visible }) => {
      const queryClient = createQueryClient()
      establishAuthenticatedSession(queryClient, { ...administratorSession, capabilities })
      server.use(
        http.get('/api/control/invitations', () =>
          HttpResponse.json({
            data: { items: [issuedInvitation], page: 1, pageSize: 20, total: 1 },
          }),
        ),
      )
      const router = createAppRouter(queryClient, ['/access/invitations'])
      render(
        <QueryClientProvider client={queryClient}>
          <RouterProvider router={router} />
        </QueryClientProvider>,
      )

      expect((await screen.findAllByText('invite-…')).length).toBeGreaterThan(0)
      await waitFor(() => {
        expect(screen.queryAllByRole('button', { name: '创建邀请' }).length > 0).toBe(visible)
        expect(screen.queryAllByRole('button', { name: '撤销' }).length > 0).toBe(visible)
      })
    },
  )
})

const administratorSession: Session = {
  userId: 'administrator-id',
  displayName: 'Administrator',
  role: 'administrator',
  capabilities: ['providers:read', 'providers:write', 'access:read', 'access:write'],
  csrfToken: 'administrator-csrf',
  expiresAt: '2026-07-21T00:00:00Z',
}

const memberSession: Session = {
  userId: 'member-id',
  displayName: 'Member',
  role: 'member',
  capabilities: ['access:read'],
  csrfToken: 'member-csrf',
  expiresAt: '2026-07-21T00:00:00Z',
}

const privateAdministratorUser: UserAccount = {
  id: 'private-user-id',
  displayName: 'Private Administrator User',
  email: 'private-administrator@example.test',
  role: 'member',
  status: 'active',
  modelCount: 2,
  keyCount: 1,
  quotaRemainingTokens: 5000,
  createdAt: '2026-07-20T00:00:00Z',
}

const administratorKey: GatewayKey = {
  id: 'administrator-key-id',
  ownerId: privateAdministratorUser.id,
  ownerName: privateAdministratorUser.displayName,
  name: 'Administrator Cached Key',
  prefix: 'lgw_admin',
  status: 'active',
  authorizedModels: [],
  createdAt: '2026-07-20T00:00:00Z',
}

const memberKey: GatewayKey = {
  id: 'member-key-id',
  ownerId: memberSession.userId,
  ownerName: memberSession.displayName,
  name: 'Member Self Key',
  prefix: 'lgw_member',
  status: 'active',
  authorizedModels: [],
  createdAt: '2026-07-20T00:00:00Z',
}

const issuedInvitation: Invitation = {
  id: 'invitation-id',
  codePrefix: 'invite-',
  role: 'member',
  status: 'issued',
  expiresAt: '2026-07-21T00:00:00Z',
  createdBy: administratorSession.displayName,
}
