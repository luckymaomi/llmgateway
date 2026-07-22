import {
  BookOpenCheck,
  Boxes,
  KeyRound,
  LayoutDashboard,
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
  activePrefix: string
}

const managementNavigation: NavigationItem[] = [
  {
    label: '总览',
    to: '/overview',
    capability: 'access:read',
    icon: LayoutDashboard,
    activePrefix: '/overview',
  },
  {
    label: 'Provider 接入',
    to: '/providers/providers',
    capability: 'providers:read',
    icon: Boxes,
    activePrefix: '/providers',
  },
  {
    label: 'Provider API Key',
    to: '/credentials',
    capability: 'credentials:read',
    icon: KeyRound,
    activePrefix: '/credentials',
  },
  {
    label: '成员与 API Key',
    to: '/access/users',
    capability: 'access:read',
    icon: UsersRound,
    activePrefix: '/access',
  },
  {
    label: '用量与额度',
    to: '/ledger/entitlements',
    capability: 'ledger:read',
    icon: BookOpenCheck,
    activePrefix: '/ledger',
  },
  {
    label: '设置',
    to: '/settings',
    capability: 'access:read',
    icon: Settings,
    activePrefix: '/settings',
  },
]

const memberNavigation: NavigationItem[] = [
  {
    label: '总览',
    to: '/overview',
    capability: 'access:read',
    icon: LayoutDashboard,
    activePrefix: '/overview',
  },
  {
    label: '我的 API Key',
    to: '/access/keys',
    capability: 'access:read',
    icon: KeyRound,
    activePrefix: '/access/keys',
  },
  {
    label: '我的用量',
    to: '/ledger/usage',
    capability: 'ledger:read',
    icon: BookOpenCheck,
    activePrefix: '/ledger/usage',
  },
  {
    label: '设置',
    to: '/settings',
    capability: 'access:read',
    icon: Settings,
    activePrefix: '/settings',
  },
]

export function navigationFor(session: Session): NavigationItem[] {
  const source = session.role === 'member' ? memberNavigation : managementNavigation
  return source.filter((item) => session.capabilities.includes(item.capability))
}

export function defaultRouteFor(
  session: Session,
): '/overview' | '/providers/providers' | '/forbidden' {
  if (session.capabilities.includes('access:read')) return '/overview'
  if (session.capabilities.includes('providers:read')) return '/providers/providers'
  return '/forbidden'
}
