import { Link } from '@tanstack/react-router'
import type { ReactNode } from 'react'

import { cn } from '@/lib/cn'

export function Page({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <main id="main-content" className={cn('page', className)}>
      {children}
    </main>
  )
}

export function PageHeader({
  title,
  description,
  actions,
  eyebrow,
}: {
  title: string
  description?: string
  actions?: ReactNode
  eyebrow?: string
}) {
  return (
    <header className="page-header">
      <div className="page-header__text">
        {eyebrow ? <span className="page-header__eyebrow">{eyebrow}</span> : null}
        <h1>{title}</h1>
        {description ? <p>{description}</p> : null}
      </div>
      {actions ? <div className="page-header__actions">{actions}</div> : null}
    </header>
  )
}

export interface SectionTab {
  label: string
  to: string
}

export function SectionTabs({ tabs }: { tabs: SectionTab[] }) {
  return (
    <nav className="section-tabs" aria-label="页面视图">
      {tabs.map((tab) => (
        <Link key={tab.to} to={tab.to} activeProps={{ 'aria-current': 'page' }}>
          {tab.label}
        </Link>
      ))}
    </nav>
  )
}

export function PageSection({
  title,
  actions,
  children,
  className,
}: {
  title?: string
  actions?: ReactNode
  children: ReactNode
  className?: string
}) {
  return (
    <section className={cn('page-section', className)}>
      {title || actions ? (
        <header className="page-section__header">
          {title ? <h2>{title}</h2> : <span />}
          {actions}
        </header>
      ) : null}
      {children}
    </section>
  )
}
