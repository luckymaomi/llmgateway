import { KeyRound } from 'lucide-react'
import { useState } from 'react'

import { useSession } from '@/app/session'
import { Page, PageHeader, PageSection } from '@/components/layout'
import { Button } from '@/components/ui/button'

import { PasswordChangeDialog } from './password-change-dialog'

export function AccountPage() {
  const session = useSession()
  const [passwordOpen, setPasswordOpen] = useState(false)
  return (
    <Page>
      <PageHeader title="账号操作" />
      <PageSection
        title="当前账号"
        actions={
          <Button icon={<KeyRound size={16} />} onClick={() => setPasswordOpen(true)}>
            更换密码
          </Button>
        }
      >
        <dl className="fact-grid">
          <div>
            <dt>显示名称</dt>
            <dd>{session.displayName}</dd>
          </div>
          <div>
            <dt>角色</dt>
            <dd>成员</dd>
          </div>
          <div>
            <dt>会话到期</dt>
            <dd>{new Date(session.expiresAt).toLocaleString('zh-CN')}</dd>
          </div>
        </dl>
      </PageSection>
      <PasswordChangeDialog open={passwordOpen} onOpenChange={setPasswordOpen} />
    </Page>
  )
}
