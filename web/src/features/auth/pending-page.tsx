import { Link } from '@tanstack/react-router'
import { Clock3 } from 'lucide-react'

import { AuthPanel } from '@/components/layout'
import { Button } from '@/components/ui/button'

export function PendingPage() {
  return (
    <AuthPanel title="等待审核">
      <div className="pending-state">
        <Clock3 size={28} />
        <span>注册信息已提交</span>
      </div>
      <Button asChild variant="secondary">
        <Link to="/login">返回登录</Link>
      </Button>
    </AuthPanel>
  )
}
