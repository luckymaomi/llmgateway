import { useSession } from '@/app/session'
import { SectionTabs } from '@/components/layout'

export function AccessTabs() {
  const session = useSession()
  return (
    <SectionTabs
      tabs={
        session.role === 'member'
          ? [{ label: '我的网关 Key', to: '/access/keys' }]
          : [
              { label: '用户', to: '/access/users' },
              { label: '邀请', to: '/access/invitations' },
              { label: '网关 Key', to: '/access/keys' },
            ]
      }
    />
  )
}
