import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Link, Outlet, useNavigate, useRouterState } from '@tanstack/react-router'
import { Dialog, Tooltip } from 'radix-ui'
import { LogOut, Menu, Network, PanelLeftClose, X } from 'lucide-react'
import { Suspense, useState } from 'react'

import { authApi } from '@/api'
import { navigationFor } from '@/app/navigation'
import { useSession } from '@/app/session'
import { cn } from '@/lib/cn'

import { IconButton } from '../ui/icon-button'
import { LoadingState } from '../ui/state'

const roleLabel = {
  administrator: '管理员',
  operator: '运维人员',
  member: '成员',
} as const

export function AppShell() {
  const session = useSession()
  const navigation = navigationFor(session)
  const [collapsed, setCollapsed] = useState(false)
  const [mobileOpen, setMobileOpen] = useState(false)
  const pathname = useRouterState({ select: (state) => state.location.pathname })
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const logout = useMutation({
    mutationFn: authApi.logout,
    async onSuccess() {
      queryClient.removeQueries({ queryKey: ['session'] })
      await navigate({ to: '/login', replace: true })
    },
  })

  const nav = (
    <SidebarNavigation
      collapsed={collapsed}
      pathname={pathname}
      navigation={navigation}
      onNavigate={() => setMobileOpen(false)}
    />
  )

  return (
    <Tooltip.Provider delayDuration={350}>
      <div className={cn('app-shell', collapsed && 'app-shell--collapsed')}>
        <aside className="sidebar" aria-label="主导航">
          <div className="sidebar__brand">
            <Network size={22} aria-hidden="true" />
            <span>LLMGateway</span>
          </div>
          {nav}
          <div className="sidebar__footer">
            <div className="sidebar__identity">
              <span className="sidebar__avatar">
                {session.displayName.slice(0, 1).toUpperCase()}
              </span>
              <span className="sidebar__identity-text">
                <strong>{session.displayName}</strong>
                <small>{roleLabel[session.role]}</small>
              </span>
            </div>
            <IconButton
              label="退出登录"
              disabled={logout.isPending}
              onClick={() => logout.mutate()}
            >
              <LogOut size={17} />
            </IconButton>
          </div>
        </aside>

        <div className="app-column">
          <header className="app-header">
            <div className="app-header__left">
              <Dialog.Root open={mobileOpen} onOpenChange={setMobileOpen}>
                <Dialog.Trigger asChild>
                  <IconButton label="打开导航" className="mobile-menu-trigger">
                    <Menu size={19} />
                  </IconButton>
                </Dialog.Trigger>
                <Dialog.Portal>
                  <Dialog.Overlay className="dialog-overlay" />
                  <Dialog.Content className="mobile-navigation">
                    <div className="mobile-navigation__header">
                      <Dialog.Title>LLMGateway</Dialog.Title>
                      <Dialog.Close asChild>
                        <IconButton label="关闭导航">
                          <X size={19} />
                        </IconButton>
                      </Dialog.Close>
                    </div>
                    {nav}
                  </Dialog.Content>
                </Dialog.Portal>
              </Dialog.Root>
              <IconButton
                label={collapsed ? '展开侧栏' : '收起侧栏'}
                className="sidebar-toggle"
                onClick={() => setCollapsed((value) => !value)}
              >
                <PanelLeftClose size={18} />
              </IconButton>
              <span className="app-header__context">控制面</span>
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
      </div>
    </Tooltip.Provider>
  )
}

function SidebarNavigation({
  collapsed,
  pathname,
  navigation,
  onNavigate,
}: {
  collapsed: boolean
  pathname: string
  navigation: ReturnType<typeof navigationFor>
  onNavigate: () => void
}) {
  return (
    <nav className="sidebar__nav">
      {navigation.map((item) => {
        const Icon = item.icon
        const active = pathname.startsWith(item.activePrefix)
        const link = (
          <Link
            key={item.to}
            to={item.to}
            className={cn('sidebar__link', active && 'sidebar__link--active')}
            aria-current={active ? 'page' : undefined}
            onClick={onNavigate}
          >
            <Icon size={18} strokeWidth={1.8} />
            <span>{item.label}</span>
          </Link>
        )
        return collapsed ? (
          <Tooltip.Root key={item.to}>
            <Tooltip.Trigger asChild>{link}</Tooltip.Trigger>
            <Tooltip.Portal>
              <Tooltip.Content side="right" sideOffset={10} className="tooltip">
                {item.label}
                <Tooltip.Arrow className="tooltip__arrow" />
              </Tooltip.Content>
            </Tooltip.Portal>
          </Tooltip.Root>
        ) : (
          link
        )
      })}
    </nav>
  )
}
