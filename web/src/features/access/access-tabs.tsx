import { useSession } from '@/app/session'
import { SectionTabs } from '@/components/layout'

export function AccessTabs() {
  const session = useSession()
  return (
    <SectionTabs
      tabs={
        session.role === 'member'
          ? [{ label: '我的 API Key', to: '/access/keys' }]
          : [
              { label: '成员', to: '/access/users' },
              { label: '邀请', to: '/access/invitations' },
              { label: 'API Key', to: '/access/keys' },
            ]
      }
    />
  )
}
