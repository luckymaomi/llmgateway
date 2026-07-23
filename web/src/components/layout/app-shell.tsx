import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, Outlet, useNavigate, useRouterState } from '@tanstack/react-router'
import { Tooltip } from 'radix-ui'
import { KeyRound, LogOut, Network } from 'lucide-react'
import { Suspense, useState } from 'react'

import { authApi, type Session } from '@/api'
import { navigationFor, navigationItemFor } from '@/app/navigation'
import { clearAuthenticatedSession, useSession } from '@/app/session'
import { siteProfileQuery } from '@/app/site-profile'
import { PasswordChangeDialog } from '@/features/auth/password-change-dialog'
import { OnboardingTourProvider } from '@/features/onboarding/onboarding-tour'
import { cn } from '@/lib/cn'

import { IconButton } from '../ui/icon-button'
import { LoadingState } from '../ui/state'

const roleLabel = {
  administrator: '管理员',
  member: '成员',
} as const

export function AppShell() {
  const session = useSession()
  const siteProfile = useQuery(siteProfileQuery)
  const siteName = siteProfile.data?.name ?? 'LLMGateway'
  const navigation = navigationFor(session)
  const [passwordOpen, setPasswordOpen] = useState(false)
  const pathname = useRouterState({ select: (state) => state.location.pathname })
  const currentPage = navigationItemFor(session, pathname)
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const logout = useMutation({
    mutationFn: authApi.logout,
    async onSettled() {
      clearAuthenticatedSession(queryClient)
      await navigate({ to: '/login', replace: true })
    },
  })

  const desktopSessionControls = (
    <SessionControls
      session={session}
      logoutPending={logout.isPending}
      onChangePassword={() => setPasswordOpen(true)}
      onLogout={() => logout.mutate()}
    />
  )

  return (
    <OnboardingTourProvider>
      <Tooltip.Provider delayDuration={350}>
        <div className="app-shell">
          <aside
            className="sidebar"
            aria-label={session.role === 'administrator' ? '管理员导航' : '控制台导航'}
          >
            <div className="sidebar__brand">
              <span className="sidebar__brand-mark" aria-hidden="true">
                <Network size={18} />
              </span>
              <span>{siteName}</span>
            </div>
            <SidebarNavigation pathname={pathname} navigation={navigation} />
            {desktopSessionControls}
          </aside>

          <div className="app-column">
            <header className="app-header">
              <div className="app-header__left">
                <strong>{currentPage?.label ?? '控制台'}</strong>
                <span className="app-header__context">
                  {session.role === 'administrator' ? '管理员控制台' : '控制台'}
                </span>
              </div>
              <div className="app-header__status">
                <span className="health-dot" aria-hidden="true" />
                会话有效
              </div>
            </header>
            <Suspense fallback={<LoadingState label="正在加载页面" />}>
              <Outlet />
            </Suspense>
          </div>
          <PasswordChangeDialog open={passwordOpen} onOpenChange={setPasswordOpen} />
        </div>
      </Tooltip.Provider>
    </OnboardingTourProvider>
  )
}

function SessionControls({
  session,
  logoutPending,
  onChangePassword,
  onLogout,
}: {
  session: Session
  logoutPending: boolean
  onChangePassword: () => void
  onLogout: () => void
}) {
  return (
    <div className="sidebar__footer">
      <div className="sidebar__identity">
        <span className="sidebar__avatar">{session.displayName.slice(0, 1).toUpperCase()}</span>
        <span className="sidebar__identity-text">
          <strong>{session.displayName}</strong>
          <small>{roleLabel[session.role]}</small>
        </span>
      </div>
      <IconButton label="更换密码" onClick={onChangePassword}>
        <KeyRound size={17} />
      </IconButton>
      <IconButton label="退出登录" disabled={logoutPending} onClick={onLogout}>
        <LogOut size={17} />
      </IconButton>
    </div>
  )
}

function SidebarNavigation({
  pathname,
  navigation,
}: {
  pathname: string
  navigation: ReturnType<typeof navigationFor>
}) {
  return (
    <nav className="sidebar__nav">
      {navigation.map((group, groupIndex) => (
        <div className="sidebar__group" key={group.label ?? `group-${groupIndex}`}>
          {group.label ? <span className="sidebar__label">{group.label}</span> : null}
          {group.items.map((item) => {
            const Icon = item.icon
            const active = pathname === item.to
            return (
              <Link
                key={item.to}
                to={item.to}
                className={cn('sidebar__link', active && 'sidebar__link--active')}
                aria-current={active ? 'page' : undefined}
                data-onboarding-nav={item.to}
              >
                <Icon size={17} strokeWidth={1.8} />
                <span>{item.label}</span>
              </Link>
            )
          })}
        </div>
      ))}
    </nav>
  )
}
