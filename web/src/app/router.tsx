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
const ResourcePoolsPage = lazyRouteComponent(
  () => import('@/features/catalog/resource-pools-page'),
  'ResourcePoolsPage',
)
const CredentialsPage = lazyRouteComponent(
  () => import('@/features/credentials/credentials-page'),
  'CredentialsPage',
)
const PlansPage = lazyRouteComponent(
  () => import('@/features/subscriptions/plans-page'),
  'PlansPage',
)
const SubscriptionsPage = lazyRouteComponent(
  () => import('@/features/subscriptions/subscriptions-page'),
  'SubscriptionsPage',
)
const UsersPage = lazyRouteComponent(() => import('@/features/access/users-page'), 'UsersPage')
const KeysPage = lazyRouteComponent(() => import('@/features/access/keys-page'), 'KeysPage')
const UsagePage = lazyRouteComponent(() => import('@/features/ledger/usage-page'), 'UsagePage')
const EntriesPage = lazyRouteComponent(
  () => import('@/features/ledger/entries-page'),
  'EntriesPage',
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
const OperationsPage = lazyRouteComponent(
  () => import('@/features/operations/operations-page'),
  'OperationsPage',
)
const GettingStartedPage = lazyRouteComponent(
  () => import('@/features/onboarding/getting-started-page'),
  'GettingStartedPage',
)
const AccountPage = lazyRouteComponent(() => import('@/features/auth/account-page'), 'AccountPage')

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

function parseLoginSearch(search: Record<string, unknown>): { redirect?: string } {
  return typeof search.redirect === 'string' ? { redirect: search.redirect } : {}
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

const resourcePoolsRoute = protectedRoute(
  '/resource-pools',
  'resource-pools:write',
  ResourcePoolsPage,
)
const credentialsRoute = protectedRoute('/provider-keys', 'credentials:write', CredentialsPage)
const plansRoute = protectedRoute('/plans', 'plans:write', PlansPage)
const subscriptionsRoute = createRoute({
  getParentRoute: () => authenticatedLayout,
  path: '/subscriptions',
  beforeLoad: async ({ context }) => {
    const session = await context.queryClient.ensureQueryData(sessionQuery)
    const allowed =
      session.capabilities.includes('subscriptions:write') ||
      session.capabilities.includes('subscriptions:read')
    if (!allowed) throw redirect({ to: '/forbidden' })
  },
  validateSearch: listSearch,
  component: SubscriptionsPage,
})
const usersRoute = protectedRoute('/members', 'members:write', UsersPage, true, ['administrator'])
const keysRoute = protectedRoute('/api-keys', 'keys:write', KeysPage, true)
const usageRoute = createRoute({
  getParentRoute: () => authenticatedLayout,
  path: '/api-logs',
  beforeLoad: usageGuard,
  validateSearch: listSearch,
  component: UsagePage,
})
const entriesRoute = createRoute({
  getParentRoute: () => authenticatedLayout,
  path: '/quota-records',
  beforeLoad: usageGuard,
  validateSearch: listSearch,
  component: EntriesPage,
})
const costsRoute = protectedRoute('/costs', 'operations:read', CostsPage, true, ['administrator'])
const settingsRoute = protectedRoute('/site-settings', 'members:write', SettingsPage, false, [
  'administrator',
])
const overviewRoute = createRoute({
  getParentRoute: () => authenticatedLayout,
  path: '/dashboard',
  component: OverviewPage,
})
const operationsRoute = protectedRoute('/operations', 'operations:read', OperationsPage, false, [
  'administrator',
])
const gettingStartedRoute = protectedRoute(
  '/getting-started',
  'operations:read',
  GettingStartedPage,
  false,
  ['administrator'],
)
const accountRoute = protectedRoute('/account', 'subscriptions:read', AccountPage, false, [
  'member',
])
const forbiddenRoute = createRoute({
  getParentRoute: () => authenticatedLayout,
  path: '/forbidden',
  component: ForbiddenPage,
})

async function usageGuard({ context }: { context: RouterContext }) {
  const session = await context.queryClient.ensureQueryData(sessionQuery)
  if (
    !session.capabilities.includes('operations:read') &&
    !session.capabilities.includes('usage:read')
  ) {
    throw redirect({ to: '/forbidden' })
  }
}

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
  publicLayout.addChildren([setupRoute, loginRoute]),
  authenticatedLayout.addChildren([
    resourcePoolsRoute,
    credentialsRoute,
    plansRoute,
    subscriptionsRoute,
    usersRoute,
    keysRoute,
    usageRoute,
    entriesRoute,
    costsRoute,
    settingsRoute,
    overviewRoute,
    operationsRoute,
    gettingStartedRoute,
    accountRoute,
    forbiddenRoute,
  ]),
])

export function createAppRouter({ queryClient, memory }: RouterContext & { memory?: boolean }) {
  return createRouter({
    routeTree,
    context: { queryClient },
    ...(memory ? { history: createMemoryHistory({ initialEntries: ['/'] }) } : {}),
  })
}

declare module '@tanstack/react-router' {
  interface Register {
    router: ReturnType<typeof createAppRouter>
  }
}
