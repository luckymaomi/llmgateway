import { hasCapability, useSession } from '@/app/session'
import { SectionTabs, type SectionTab } from '@/components/layout'

export function OperationsTabs() {
  const session = useSession()
  const tabs: SectionTab[] = []
  if (hasCapability(session, 'operations:read'))
    tabs.push({ label: '请求', to: '/operations/requests' })
  if (hasCapability(session, 'audit:read'))
    tabs.push({ label: '管理审计', to: '/operations/audit' })
  if (hasCapability(session, 'content:read'))
    tabs.push({ label: '内容留存', to: '/operations/content' })
  return <SectionTabs tabs={tabs} />
}
