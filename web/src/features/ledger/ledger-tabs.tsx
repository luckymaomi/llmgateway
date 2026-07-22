import { useSession } from '@/app/session'
import { SectionTabs } from '@/components/layout'

export function LedgerTabs() {
  const session = useSession()
  return (
    <SectionTabs
      tabs={
        session.role === 'member'
          ? [{ label: '我的用量', to: '/ledger/usage' }]
          : [
              { label: '请求用量', to: '/ledger/usage' },
              { label: '额度变更记录', to: '/ledger/entries' },
              { label: '成员额度', to: '/ledger/entitlements' },
              { label: '上游成本', to: '/ledger/costs' },
            ]
      }
    />
  )
}
