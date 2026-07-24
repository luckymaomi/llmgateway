import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { Activity, AlertTriangle, Check, Clock3, KeyRound } from 'lucide-react'

import { catalogApi, operationsApi } from '@/api'
import { Page, PageHeader, PageSection } from '@/components/layout'
import { StatusBadge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { ErrorState, LoadingState } from '@/components/ui/state'
import { formatDateTime, formatDuration, formatNumber, formatPercent } from '@/lib/format'

import {
  CredentialStatusChart,
  RequestErrorChart,
  RequestOutcomeChart,
  RequestTrendChart,
} from '../overview/operations-charts'

export function OperationsPage() {
  const overview = useQuery({
    queryKey: ['operations-overview'],
    queryFn: ({ signal }) => operationsApi.overview(signal),
    refetchInterval: 30_000,
  })
  const credentials = useQuery({
    queryKey: ['credentials', 'operations'],
    queryFn: ({ signal }) => catalogApi.credentials(true, signal),
    refetchInterval: 30_000,
  })
  if (overview.isLoading)
    return (
      <Page>
        <LoadingState label="正在读取运行状态" />
      </Page>
    )
  if (overview.error || !overview.data || overview.data.scope !== 'administrator')
    return (
      <Page>
        <ErrorState
          error={overview.error ?? new Error('运行状态不可用')}
          onRetry={() => void overview.refetch()}
        />
      </Page>
    )

  const requestCount = overview.data.requests.requestCount
  const successRate = requestCount
    ? overview.data.requests.completedCount / requestCount
    : undefined
  const attention = (credentials.data ?? []).filter(
    (credential) =>
      credential.status !== 'active' ||
      credential.cooldownUntil !== undefined ||
      ['failed', 'unavailable'].includes(credential.lastProbeStatus ?? ''),
  )
  return (
    <Page>
      <PageHeader
        title="运行状态"
        actions={
          <Button asChild variant="secondary" size="sm">
            <Link to="/api-logs">查看 API 日志</Link>
          </Button>
        }
      />
      <div className="summary-grid">
        <Metric
          icon={<Activity size={16} />}
          label="24 小时请求"
          value={formatNumber(requestCount)}
        />
        <Metric icon={<Check size={16} />} label="完成率" value={formatPercent(successRate)} />
        <Metric
          icon={<Clock3 size={16} />}
          label="首字节 P95"
          value={formatDuration(overview.data.requests.firstByteP95Ms)}
        />
        <Metric
          icon={<KeyRound size={16} />}
          label="可用上游 API Key"
          value={`${overview.data.resources.activeCredentialCount} / ${overview.data.resources.credentialCount}`}
        />
      </div>
      <PageSection title="24 小时趋势">
        <RequestTrendChart overview={overview.data} />
      </PageSection>
      <div className="chart-grid">
        <PageSection title="请求终态">
          <RequestOutcomeChart overview={overview.data} />
        </PageSection>
        <PageSection title="上游 API Key 状态">
          <CredentialStatusChart overview={overview.data} />
        </PageSection>
      </div>
      <PageSection title="24 小时错误分布">
        <RequestErrorChart overview={overview.data} />
      </PageSection>
      <PageSection
        title="需要处理的上游 API Key"
        actions={
          <Button asChild variant="quiet" size="sm">
            <Link to="/provider-keys">管理 Key</Link>
          </Button>
        }
      >
        {credentials.error ? (
          <ErrorState error={credentials.error} onRetry={() => void credentials.refetch()} />
        ) : credentials.isLoading ? (
          <LoadingState label="正在读取上游 API Key" />
        ) : attention.length === 0 ? (
          <div className="quiet-result">
            <Check size={18} />
            <span>当前没有需要处理的上游 API Key</span>
          </div>
        ) : (
          <div className="alert-list">
            {attention.map((credential) => (
              <div className="alert-row" key={credential.id}>
                <AlertTriangle size={17} aria-hidden="true" />
                <div>
                  <div className="alert-row__title">
                    <strong>{credential.name}</strong>
                    <StatusBadge status={credential.status} />
                  </div>
                  <p>
                    {credential.resourcePoolName} · {credential.providerName}
                    {credential.lastProbeErrorKind ? ` · ${credential.lastProbeErrorKind}` : ''}
                  </p>
                  <small>
                    {credential.cooldownUntil
                      ? `冷却至 ${formatDateTime(credential.cooldownUntil)}`
                      : credential.lastProbeAt
                        ? `最近探测 ${formatDateTime(credential.lastProbeAt)}`
                        : '尚未探测'}
                  </small>
                </div>
              </div>
            ))}
          </div>
        )}
      </PageSection>
    </Page>
  )
}

function Metric({ icon, label, value }: { icon: React.ReactNode; label: string; value: string }) {
  return (
    <div className="summary-metric">
      <span className="summary-metric__label">
        {icon}
        {label}
      </span>
      <strong>{value}</strong>
    </div>
  )
}
