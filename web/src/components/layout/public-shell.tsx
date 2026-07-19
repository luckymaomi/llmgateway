import { Outlet } from '@tanstack/react-router'
import { Network } from 'lucide-react'
import { Suspense, type ReactNode } from 'react'

import { LoadingState } from '../ui/state'

export function PublicShell() {
  return (
    <div className="public-shell">
      <header className="public-brand">
        <Network size={24} />
        <span>LLMGateway</span>
      </header>
      <main className="public-main">
        <Suspense fallback={<LoadingState label="正在加载页面" />}>
          <Outlet />
        </Suspense>
      </main>
    </div>
  )
}

export function AuthPanel({
  title,
  subtitle,
  children,
}: {
  title: string
  subtitle?: string
  children: ReactNode
}) {
  return (
    <section className="auth-panel">
      <header>
        <h1>{title}</h1>
        {subtitle ? <p>{subtitle}</p> : null}
      </header>
      {children}
    </section>
  )
}
