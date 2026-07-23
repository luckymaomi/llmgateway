import {
  Activity,
  CircleUserRound,
  CircleDollarSign,
  FileClock,
  KeyRound,
  LayoutDashboard,
  PackageCheck,
  Route,
  ScrollText,
  ServerCog,
  Settings,
  UsersRound,
  WalletCards,
  type LucideIcon,
} from 'lucide-react'

import type { Capability, Session } from '@/api'

export interface NavigationItem {
  label: string
  to: string
  capability: Capability
  icon: LucideIcon
}

export interface NavigationGroup {
  label?: string
  items: NavigationItem[]
}

const administratorNavigation: NavigationGroup[] = [
  {
    label: '工作台',
    items: [
      { label: '新手指引', to: '/getting-started', capability: 'operations:read', icon: Route },
      { label: '仪表盘', to: '/dashboard', capability: 'operations:read', icon: LayoutDashboard },
      { label: '运行状态', to: '/operations', capability: 'operations:read', icon: Activity },
    ],
  },
  {
    label: '上游资源',
    items: [
      {
        label: '资源池',
        to: '/resource-pools',
        capability: 'resource-pools:write',
        icon: ServerCog,
      },
      {
        label: '上游 API Key',
        to: '/provider-keys',
        capability: 'credentials:write',
        icon: KeyRound,
      },
    ],
  },
  {
    label: '成员服务',
    items: [
      { label: '套餐', to: '/plans', capability: 'plans:write', icon: PackageCheck },
      {
        label: '订阅',
        to: '/subscriptions',
        capability: 'subscriptions:write',
        icon: WalletCards,
      },
      { label: '成员', to: '/members', capability: 'members:write', icon: UsersRound },
      { label: 'API 密钥', to: '/api-keys', capability: 'keys:write', icon: KeyRound },
    ],
  },
  {
    label: '运营数据',
    items: [
      { label: 'API 日志', to: '/api-logs', capability: 'operations:read', icon: ScrollText },
      {
        label: '额度记录',
        to: '/quota-records',
        capability: 'operations:read',
        icon: FileClock,
      },
      {
        label: '上游成本',
        to: '/costs',
        capability: 'operations:read',
        icon: CircleDollarSign,
      },
    ],
  },
  {
    label: '系统',
    items: [
      { label: '站点设置', to: '/site-settings', capability: 'members:write', icon: Settings },
    ],
  },
]

const memberNavigation: NavigationGroup[] = [
  {
    label: '我的服务',
    items: [
      {
        label: '仪表盘',
        to: '/dashboard',
        capability: 'subscriptions:read',
        icon: LayoutDashboard,
      },
      {
        label: '我的订阅',
        to: '/subscriptions',
        capability: 'subscriptions:read',
        icon: WalletCards,
      },
      { label: 'API 密钥', to: '/api-keys', capability: 'keys:write', icon: KeyRound },
      { label: 'API 日志', to: '/api-logs', capability: 'usage:read', icon: ScrollText },
      { label: '额度记录', to: '/quota-records', capability: 'usage:read', icon: FileClock },
      {
        label: '账号操作',
        to: '/account',
        capability: 'subscriptions:read',
        icon: CircleUserRound,
      },
    ],
  },
]

export function navigationFor(session: Session): NavigationGroup[] {
  const source = session.role === 'member' ? memberNavigation : administratorNavigation
  return source
    .map((group) => ({
      ...group,
      items: group.items.filter((item) => session.capabilities.includes(item.capability)),
    }))
    .filter((group) => group.items.length > 0)
}

export function navigationItemFor(session: Session, pathname: string): NavigationItem | undefined {
  return navigationFor(session)
    .flatMap((group) => group.items)
    .find((item) => pathname === item.to)
}

export function defaultRouteFor(session: Session): '/dashboard' | '/resource-pools' | '/forbidden' {
  if (
    session.capabilities.includes('operations:read') ||
    session.capabilities.includes('subscriptions:read')
  ) {
    return '/dashboard'
  }
  if (session.capabilities.includes('resource-pools:write')) return '/resource-pools'
  return '/forbidden'
}
