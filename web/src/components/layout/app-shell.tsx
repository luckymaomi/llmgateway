import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, Outlet, useNavigate, useRouterState } from '@tanstack/react-router'
import { Dialog, Tooltip } from 'radix-ui'
import { KeyRound, LogOut, Menu, Network, X } from 'lucide-react'
import { Suspense, useState } from 'react'

import { authApi, type Session } from '@/api'
import { navigationFor, navigationItemFor } from '@/app/navigation'
import { clearAuthenticatedSession, useSession } from '@/app/session'
import { siteProfileQuery } from '@/app/site-profile'
import { PasswordChangeDialog } from '@/features/auth/password-change-dialog'
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
  const [mobileOpen, setMobileOpen] = useState(false)
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

  const navigationContent = (
    <SidebarNavigation
      pathname={pathname}
      navigation={navigation}
      onNavigate={() => setMobileOpen(false)}
    />
  )
  const sessionControls = (
    <SessionControls
      session={session}
      logoutPending={logout.isPending}
      onChangePassword={() => {
        setMobileOpen(false)
        setPasswordOpen(true)
      }}
      onLogout={() => logout.mutate()}
    />
  )

  return (
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
          {navigationContent}
          {sessionControls}
        </aside>

        <div className="app-column">
          <header className="app-header">
            <div className="app-header__left">
              <Dialog.Root open={mobileOpen} onOpenChange={setMobileOpen}>
                <Dialog.Trigger asChild>
                  <IconButton label="打开导航" className="mobile-menu-trigger" showTooltip={false}>
                    <Menu size={19} />
                  </IconButton>
                </Dialog.Trigger>
                <Dialog.Portal>
                  <Dialog.Overlay className="dialog-overlay" />
                  <Dialog.Content className="mobile-navigation">
                    <div className="mobile-navigation__header">
                      <Dialog.Title>{siteName}</Dialog.Title>
                      <Dialog.Close asChild>
                        <IconButton label="关闭导航" showTooltip={false}>
                          <X size={19} />
                        </IconButton>
                      </Dialog.Close>
                    </div>
                    {navigationContent}
                    {sessionControls}
                  </Dialog.Content>
                </Dialog.Portal>
              </Dialog.Root>
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
  onNavigate,
}: {
  pathname: string
  navigation: ReturnType<typeof navigationFor>
  onNavigate: () => void
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
                onClick={onNavigate}
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
