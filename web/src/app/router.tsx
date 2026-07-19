/* eslint-disable @typescript-eslint/only-throw-error -- TanStack Router models redirects as thrown responses. */
import type { QueryClient } from '@tanstack/react-query'
import {
  Outlet,
  createMemoryHistory,
  createRootRouteWithContext,
  createRoute,
  createRouter,
  lazyRouteComponent,
  redirect,
  type AsyncRouteComponent,
} from '@tanstack/react-router'

import { ApiProblem, type Capability } from '@/api'
import { AppShell, PublicShell } from '@/components/layout'
import { ForbiddenPage, NotFoundPage, RouteErrorPage } from '@/features/errors/error-pages'

import { sessionQuery } from './session'

const SetupPage = lazyRouteComponent(() => import('@/features/auth/setup-page'), 'SetupPage')
const LoginPage = lazyRouteComponent(() => import('@/features/auth/login-page'), 'LoginPage')
const RegisterPage = lazyRouteComponent(
  () => import('@/features/auth/register-page'),
  'RegisterPage',
)
const PendingPage = lazyRouteComponent(() => import('@/features/auth/pending-page'), 'PendingPage')

const OverviewPage = lazyRouteComponent(
  () => import('@/features/overview/overview-page'),
  'OverviewPage',
)
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
const RequestsPage = lazyRouteComponent(
  () => import('@/features/operations/requests-page'),
  'RequestsPage',
)
const AuditPage = lazyRouteComponent(() => import('@/features/operations/audit-page'), 'AuditPage')
const ContentPage = lazyRouteComponent(
  () => import('@/features/operations/content-page'),
  'ContentPage',
)
const PlaygroundPage = lazyRouteComponent(
  () => import('@/features/playground/playground-page'),
  'PlaygroundPage',
)
const SettingsPage = lazyRouteComponent(
  () => import('@/features/settings/settings-page'),
  'SettingsPage',
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
      throw redirect({ to: session.role === 'member' ? '/playground' : '/overview' })
    } catch (error) {
      if (isRedirect(error)) throw error
      throw redirect({ to: '/login' })
    }
  },
})

const setupRoute = createRoute({
  getParentRoute: () => publicLayout,
  path: '/setup',
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
) {
  return createRoute({
    getParentRoute: () => authenticatedLayout,
    path,
    beforeLoad: guard(capability),
    ...(withSearch ? { validateSearch: listSearch } : {}),
    component,
  })
}

const overviewRoute = protectedRoute('/overview', 'overview:read', OverviewPage)
const providersRoute = protectedRoute('/providers/providers', 'providers:read', ProvidersPage, true)
const modelsRoute = protectedRoute('/providers/models', 'providers:read', ModelsPage, true)
const revisionsRoute = protectedRoute('/providers/revisions', 'providers:read', RevisionsPage, true)
const credentialsRoute = protectedRoute('/credentials', 'credentials:read', CredentialsPage, true)
const usersRoute = protectedRoute('/access/users', 'access:read', UsersPage, true)
const invitationsRoute = protectedRoute('/access/invitations', 'access:read', InvitationsPage, true)
const keysRoute = protectedRoute('/access/keys', 'access:read', KeysPage, true)
const usageRoute = protectedRoute('/ledger/usage', 'ledger:read', UsagePage, true)
const entriesRoute = protectedRoute('/ledger/entries', 'ledger:read', EntriesPage, true)
const entitlementsRoute = protectedRoute(
  '/ledger/entitlements',
  'ledger:read',
  EntitlementsPage,
  true,
)
const requestsRoute = protectedRoute('/operations/requests', 'operations:read', RequestsPage, true)
const auditRoute = protectedRoute('/operations/audit', 'audit:read', AuditPage, true)
const contentRoute = protectedRoute('/operations/content', 'content:read', ContentPage, true)
const playgroundRoute = protectedRoute('/playground', 'playground:use', PlaygroundPage)
const securityRoute = protectedRoute('/settings/security', 'settings:read', () => (
  <SettingsPage section="security" />
))
const networkRoute = protectedRoute('/settings/network', 'settings:read', () => (
  <SettingsPage section="network" />
))
const observabilityRoute = protectedRoute('/settings/observability', 'settings:read', () => (
  <SettingsPage section="observability" />
))
const backupsRoute = protectedRoute('/settings/backups', 'settings:read', () => (
  <SettingsPage section="backups" />
))
const settingsRevisionsRoute = protectedRoute('/settings/revisions', 'settings:read', () => (
  <SettingsPage section="revisions" />
))
const forbiddenRoute = createRoute({
  getParentRoute: () => authenticatedLayout,
  path: '/forbidden',
  component: ForbiddenPage,
})

function guard(capability: Capability) {
  return async ({ context }: { context: RouterContext }) => {
    const session = await context.queryClient.ensureQueryData(sessionQuery)
    if (!session.capabilities.includes(capability)) throw redirect({ to: '/forbidden' })
  }
}

const routeTree = rootRoute.addChildren([
  rootIndex,
  publicLayout.addChildren([setupRoute, loginRoute, registerRoute, pendingRoute]),
  authenticatedLayout.addChildren([
    overviewRoute,
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
    requestsRoute,
    auditRoute,
    contentRoute,
    playgroundRoute,
    securityRoute,
    networkRoute,
    observabilityRoute,
    backupsRoute,
    settingsRevisionsRoute,
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

function isRedirect(error: unknown): boolean {
  return typeof error === 'object' && error !== null && 'isRedirect' in error
}

export type AppRouter = ReturnType<typeof createAppRouter>

declare module '@tanstack/react-router' {
  interface Register {
    router: AppRouter
  }
}
