import { Link } from '@tanstack/react-router'
import { Ban, CircleAlert, SearchX } from 'lucide-react'

import { Page } from '@/components/layout'
import { Button } from '@/components/ui/button'

export function ForbiddenPage() {
  return (
    <Page className="route-result">
      <Ban size={32} />
      <h1>当前会话无权执行此任务</h1>
      <Button asChild variant="secondary">
        <Link to="/">返回可用入口</Link>
      </Button>
    </Page>
  )
}

export function NotFoundPage() {
  return (
    <Page className="route-result">
      <SearchX size={32} />
      <h1>没有找到该页面</h1>
      <Button asChild variant="secondary">
        <Link to="/">返回入口</Link>
      </Button>
    </Page>
  )
}

export function RouteErrorPage({ error, reset }: { error: Error; reset: () => void }) {
  return (
    <Page className="route-result">
      <CircleAlert size={32} />
      <h1>页面未完成加载</h1>
      <p>{error.message}</p>
      <Button onClick={reset}>重试</Button>
    </Page>
  )
}
