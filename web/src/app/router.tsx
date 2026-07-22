/* eslint-disable @typescript-eslint/only-throw-error -- TanStack Router models redirects as thrown responses. */
import type { QueryClient } from '@tanstack/react-query'
import {
  Outlet,
  createMemoryHistory,
  createRootRouteWithContext,
  createRoute,
  createRouter,
  isRedirect,
  lazyRouteComponent,
  redirect,
  type AsyncRouteComponent,
} from '@tanstack/react-router'

import { ApiProblem, authApi, type Capability, type Role } from '@/api'
import { AppShell, PublicShell } from '@/components/layout'
import { ForbiddenPage, NotFoundPage, RouteErrorPage } from '@/features/errors/error-pages'

import { defaultRouteFor } from './navigation'
import { sessionQuery } from './session'

const SetupPage = lazyRouteComponent(() => import('@/features/auth/setup-page'), 'SetupPage')
const LoginPage = lazyRouteComponent(() => import('@/features/auth/login-page'), 'LoginPage')
const RegisterPage = lazyRouteComponent(
  () => import('@/features/auth/register-page'),
  'RegisterPage',
)
const PendingPage = lazyRouteComponent(() => import('@/features/auth/pending-page'), 'PendingPage')

const ProvidersPage = lazyRouteComponent(
  () => import('@/features/catalog/providers-page'),
  'ProvidersPage',
)
const ModelsPage = lazyRouteComponent(() => import('@/features/catalog/models-page'), 'ModelsPage')
const RevisionsPage = lazyRouteComponent(
  () => import('@/features/catalog/revisions-page'),
  'RevisionsPage',
)
const CredentialsPage = lazyRouteComponent(
  () => import('@/features/credentials/credentials-page'),
  'CredentialsPage',
)
const UsersPage = lazyRouteComponent(() => import('@/features/access/users-page'), 'UsersPage')
const InvitationsPage = lazyRouteComponent(
  () => import('@/features/access/invitations-page'),
  'InvitationsPage',
)
const KeysPage = lazyRouteComponent(() => import('@/features/access/keys-page'), 'KeysPage')
const UsagePage = lazyRouteComponent(() => import('@/features/ledger/usage-page'), 'UsagePage')
const EntriesPage = lazyRouteComponent(
  () => import('@/features/ledger/entries-page'),
  'EntriesPage',
)
const EntitlementsPage = lazyRouteComponent(
  () => import('@/features/ledger/entitlements-page'),
  'EntitlementsPage',
)
const CostsPage = lazyRouteComponent(() => import('@/features/ledger/costs-page'), 'CostsPage')
const SettingsPage = lazyRouteComponent(
  () => import('@/features/settings/settings-page'),
  'SettingsPage',
)
const OverviewPage = lazyRouteComponent(
  () => import('@/features/overview/overview-page'),
  'OverviewPage',
)

interface RouterContext {
  queryClient: QueryClient
}

const rootRoute = createRootRouteWithContext<RouterContext>()({
  component: Outlet,
  notFoundComponent: NotFoundPage,
  errorComponent: ({ error, reset }) => <RouteErrorPage error={error} reset={reset} />,
})

const publicLayout = createRoute({
  getParentRoute: () => rootRoute,
  id: '_public',
  component: PublicShell,
})
const authenticatedLayout = createRoute({
  getParentRoute: () => rootRoute,
  id: '_authenticated',
  beforeLoad: async ({ context, location }) => {
    try {
      await context.queryClient.ensureQueryData(sessionQuery)
    } catch (error) {
      if (error instanceof ApiProblem && error.status !== 401) throw error
      throw redirect({ to: '/login', search: { redirect: location.href } })
    }
  },
  component: AppShell,
})

const rootIndex = createRoute({
  getParentRoute: () => rootRoute,
  path: '/',
  beforeLoad: async ({ context }) => {
    try {
      const session = await context.queryClient.ensureQueryData(sessionQuery)
      throw redirect({ to: defaultRouteFor(session) })
    } catch (error) {
      if (isRedirect(error)) throw error
      if (error instanceof ApiProblem && error.status !== 401) throw error
      const setup = await authApi.setupStatus()
      throw redirect({ to: setup.required ? '/setup' : '/login' })
    }
  },
})

const setupRoute = createRoute({
  getParentRoute: () => publicLayout,
  path: '/setup',
  beforeLoad: async () => {
    const setup = await authApi.setupStatus()
    if (!setup.required) throw redirect({ to: '/' })
  },
  component: SetupPage,
})
const loginRoute = createRoute({
  getParentRoute: () => publicLayout,
  path: '/login',
  validateSearch: parseLoginSearch,
  component: () => {
    const search = loginRoute.useSearch()
    return <LoginPage {...(search.redirect ? { redirectTo: search.redirect } : {})} />
  },
})
const registerRoute = createRoute({
  getParentRoute: () => publicLayout,
  path: '/register',
  validateSearch: parseRegistrationSearch,
  component: () => <RegisterPage invitation={registerRoute.useSearch().invitation ?? ''} />,
})
const pendingRoute = createRoute({
  getParentRoute: () => publicLayout,
  path: '/pending-review',
  component: PendingPage,
})

function parseLoginSearch(search: Record<string, unknown>): { redirect?: string } {
  return typeof search.redirect === 'string' ? { redirect: search.redirect } : {}
}

function parseRegistrationSearch(search: Record<string, unknown>): { invitation?: string } {
  return typeof search.invitation === 'string' ? { invitation: search.invitation } : {}
}

interface ParsedListSearch {
  page?: number
  pageSize?: number
  search?: string
  status?: string
}

function listSearch(search: Record<string, unknown>): ParsedListSearch {
  const page = positiveInteger(search.page)
  const pageSize = boundedInteger(search.pageSize, 10, 100)
  return {
    ...(page !== undefined ? { page } : {}),
    ...(pageSize !== undefined ? { pageSize } : {}),
    ...(typeof search.search === 'string' && search.search ? { search: search.search } : {}),
    ...(typeof search.status === 'string' && search.status ? { status: search.status } : {}),
  }
}

function positiveInteger(value: unknown): number | undefined {
  const parsed = typeof value === 'number' ? value : Number(value)
  return Number.isInteger(parsed) && parsed > 0 ? parsed : undefined
}

function boundedInteger(value: unknown, minimum: number, maximum: number): number | undefined {
  const parsed = positiveInteger(value)
  return parsed !== undefined && parsed >= minimum && parsed <= maximum ? parsed : undefined
}

function protectedRoute<const TPath extends string>(
  path: TPath,
  capability: Capability,
  component: AsyncRouteComponent<unknown>,
  withSearch = false,
  roles?: readonly Role[],
) {
  return createRoute({
    getParentRoute: () => authenticatedLayout,
    path,
    beforeLoad: guard(capability, roles),
    ...(withSearch ? { validateSearch: listSearch } : {}),
    component,
  })
}

const providersRoute = protectedRoute('/providers/providers', 'providers:read', ProvidersPage, true)
const modelsRoute = protectedRoute('/providers/models', 'providers:read', ModelsPage, true)
const revisionsRoute = protectedRoute('/providers/revisions', 'providers:read', RevisionsPage, true)
const credentialsRoute = protectedRoute('/credentials', 'credentials:read', CredentialsPage, true)
const usersRoute = protectedRoute('/access/users', 'access:read', UsersPage, true, [
  'administrator',
])
const invitationsRoute = protectedRoute(
  '/access/invitations',
  'access:read',
  InvitationsPage,
  true,
  ['administrator'],
)
const keysRoute = protectedRoute('/access/keys', 'access:read', KeysPage, true, [
  'administrator',
  'member',
])
const usageRoute = protectedRoute('/ledger/usage', 'ledger:read', UsagePage, true)
const entriesRoute = protectedRoute('/ledger/entries', 'ledger:read', EntriesPage, true)
const entitlementsRoute = protectedRoute(
  '/ledger/entitlements',
  'ledger:read',
  EntitlementsPage,
  true,
  ['administrator'],
)
const costsRoute = protectedRoute('/ledger/costs', 'ledger:write', CostsPage, true, [
  'administrator',
])
const settingsRoute = protectedRoute('/settings', 'access:read', SettingsPage)
const overviewRoute = protectedRoute('/overview', 'access:read', OverviewPage)
const forbiddenRoute = createRoute({
  getParentRoute: () => authenticatedLayout,
  path: '/forbidden',
  component: ForbiddenPage,
})

function guard(capability: Capability, roles?: readonly Role[]) {
  return async ({ context }: { context: RouterContext }) => {
    const session = await context.queryClient.ensureQueryData(sessionQuery)
    if (
      !session.capabilities.includes(capability) ||
      (roles !== undefined && !roles.includes(session.role))
    ) {
      throw redirect({ to: '/forbidden' })
    }
  }
}

const routeTree = rootRoute.addChildren([
  rootIndex,
  publicLayout.addChildren([setupRoute, loginRoute, registerRoute, pendingRoute]),
  authenticatedLayout.addChildren([
    providersRoute,
    modelsRoute,
    revisionsRoute,
    credentialsRoute,
    usersRoute,
    invitationsRoute,
    keysRoute,
    usageRoute,
    entriesRoute,
    entitlementsRoute,
    costsRoute,
    settingsRoute,
    overviewRoute,
    forbiddenRoute,
  ]),
])

export function createAppRouter(queryClient: QueryClient, initialEntries?: string[]) {
  return createRouter({
    routeTree,
    context: { queryClient },
    defaultPreload: 'intent',
    defaultPreloadStaleTime: 0,
    ...(initialEntries ? { history: createMemoryHistory({ initialEntries }) } : {}),
  })
}

export type AppRouter = ReturnType<typeof createAppRouter>

declare module '@tanstack/react-router' {
  interface Register {
    router: AppRouter
  }
}
