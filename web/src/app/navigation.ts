import {
  Activity,
  Blocks,
  BookOpenCheck,
  Boxes,
  CircleDollarSign,
  FileClock,
  Gauge,
  KeyRound,
  LayoutDashboard,
  Mail,
  Rocket,
  ScrollText,
  Settings,
  UsersRound,
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
    label: '控制台',
    items: [
      { label: '仪表盘', to: '/dashboard', capability: 'access:read', icon: LayoutDashboard },
      { label: '运维监控', to: '/operations', capability: 'access:read', icon: Activity },
    ],
  },
  {
    label: '上游资源',
    items: [
      { label: 'Provider', to: '/providers', capability: 'providers:read', icon: Boxes },
      { label: '模型', to: '/models', capability: 'providers:read', icon: Blocks },
      {
        label: '上游 API Key',
        to: '/provider-keys',
        capability: 'credentials:read',
        icon: KeyRound,
      },
      {
        label: '配置发布',
        to: '/configuration',
        capability: 'providers:read',
        icon: Rocket,
      },
    ],
  },
  {
    label: '成员服务',
    items: [
      { label: '成员', to: '/members', capability: 'access:read', icon: UsersRound },
      { label: '邀请', to: '/invitations', capability: 'access:read', icon: Mail },
      {
        label: 'Gateway Key',
        to: '/gateway-keys',
        capability: 'access:read',
        icon: KeyRound,
      },
      {
        label: '订阅与额度',
        to: '/entitlements',
        capability: 'ledger:read',
        icon: Gauge,
      },
    ],
  },
  {
    label: '运营数据',
    items: [
      { label: 'API 日志', to: '/api-logs', capability: 'ledger:read', icon: ScrollText },
      {
        label: '额度记录',
        to: '/quota-records',
        capability: 'ledger:read',
        icon: FileClock,
      },
      {
        label: '上游成本',
        to: '/costs',
        capability: 'ledger:write',
        icon: CircleDollarSign,
      },
    ],
  },
  {
    label: '系统',
    items: [{ label: '站点设置', to: '/site-settings', capability: 'access:read', icon: Settings }],
  },
]

const memberNavigation: NavigationGroup[] = [
  {
    items: [
      { label: '仪表盘', to: '/dashboard', capability: 'access:read', icon: LayoutDashboard },
      {
        label: '订阅管理',
        to: '/entitlements',
        capability: 'ledger:read',
        icon: BookOpenCheck,
      },
      {
        label: '额度记录',
        to: '/quota-records',
        capability: 'ledger:read',
        icon: FileClock,
      },
      {
        label: 'Key 管理',
        to: '/gateway-keys',
        capability: 'access:read',
        icon: KeyRound,
      },
      { label: 'API 日志', to: '/api-logs', capability: 'ledger:read', icon: ScrollText },
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

export function defaultRouteFor(session: Session): '/dashboard' | '/providers' | '/forbidden' {
  if (session.capabilities.includes('access:read')) return '/dashboard'
  if (session.capabilities.includes('providers:read')) return '/providers'
  return '/forbidden'
}
