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
              { label: '账本事件', to: '/ledger/entries' },
              { label: '额度与套餐', to: '/ledger/entitlements' },
              { label: '成本', to: '/ledger/costs' },
            ]
      }
    />
  )
}
