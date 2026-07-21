import {
  BookOpenCheck,
  Boxes,
  FlaskConical,
  KeyRound,
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
    label: 'Provider 与模型',
    to: '/providers/providers',
    capability: 'providers:read',
    icon: Boxes,
    activePrefix: '/providers',
  },
  {
    label: '上游凭据池',
    to: '/credentials',
    capability: 'credentials:read',
    icon: KeyRound,
    activePrefix: '/credentials',
  },
  {
    label: '用户与网关 Key',
    to: '/access/users',
    capability: 'access:read',
    icon: UsersRound,
    activePrefix: '/access',
  },
  {
    label: '用量与账本',
    to: '/ledger/entitlements',
    capability: 'ledger:read',
    icon: BookOpenCheck,
    activePrefix: '/ledger',
  },
  {
    label: 'Playground',
    to: '/playground',
    capability: 'playground:use',
    icon: FlaskConical,
    activePrefix: '/playground',
  },
]

const memberNavigation: NavigationItem[] = [
  {
    label: 'Playground',
    to: '/playground',
    capability: 'playground:use',
    icon: FlaskConical,
    activePrefix: '/playground',
  },
  {
    label: '我的网关 Key',
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
]

export function navigationFor(session: Session): NavigationItem[] {
  const source = session.role === 'member' ? memberNavigation : managementNavigation
  return source.filter((item) => session.capabilities.includes(item.capability))
}

export function defaultRouteFor(
  session: Session,
): '/providers/providers' | '/access/keys' | '/forbidden' {
  if (session.capabilities.includes('providers:read')) return '/providers/providers'
  if (session.capabilities.includes('access:read')) return '/access/keys'
  return '/forbidden'
}
