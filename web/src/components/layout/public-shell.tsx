import { Outlet } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { Network } from 'lucide-react'
import { Suspense, type ReactNode } from 'react'

import { siteProfileQuery } from '@/app/site-profile'

import { LoadingState } from '../ui/state'

export function PublicShell() {
  const siteProfile = useQuery(siteProfileQuery)
  return (
    <div className="public-shell">
      <header className="public-brand">
        <Network size={24} />
        <span>{siteProfile.data?.name ?? 'LLMGateway'}</span>
      </header>
      <main className="public-main">
        <Suspense fallback={<LoadingState label="正在加载页面" />}>
          <Outlet />
        </Suspense>
      </main>
    </div>
  )
}

export function AuthPanel({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="auth-panel">
      <header>
        <h1>{title}</h1>
      </header>
      {children}
    </section>
  )
}
