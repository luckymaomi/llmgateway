import { useQuery } from '@tanstack/react-query'
import { AlertTriangle, CheckCircle2, Clock3, Gauge, Layers3, Workflow } from 'lucide-react'
import {
  Area,
  AreaChart,
  CartesianGrid,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'

import { apiClient, type Overview } from '@/api'
import { Page, PageHeader, PageSection } from '@/components/layout'
import { Badge, StatusBadge } from '@/components/ui/badge'
import { ErrorState, LoadingState } from '@/components/ui/state'
import { formatDateTime, formatDuration, formatNumber, formatPercent } from '@/lib/format'

const overviewQuery = () => apiClient.request<Overview>('/api/control/overview')

export function OverviewPage() {
  const query = useQuery({
    queryKey: ['overview'],
    queryFn: overviewQuery,
    refetchInterval: 30_000,
  })

  if (query.isLoading) return <LoadingState label="正在加载系统总览" />
  if (query.error || !query.data)
    return <ErrorState error={query.error} onRetry={() => void query.refetch()} />

  const overview = query.data
  return (
    <Page>
      <PageHeader
        title="总览"
        description="当前请求、资源池与故障事实"
        actions={<StatusBadge status={overview.health} />}
      />

      <section className="summary-grid" aria-label="关键指标">
        <SummaryMetric
          icon={<Workflow size={18} />}
          label="24 小时请求"
          value={formatNumber(overview.requests24h)}
        />
        <SummaryMetric
          icon={<CheckCircle2 size={18} />}
          label="成功率"
          value={formatPercent(overview.successRate)}
        />
        <SummaryMetric
          icon={<Gauge size={18} />}
          label="P95 延迟"
          value={formatDuration(overview.p95LatencyMs)}
        />
        <SummaryMetric
          icon={<Clock3 size={18} />}
          label="排队请求"
          value={formatNumber(overview.queuedRequests)}
        />
      </section>

      <div className="overview-grid">
        <PageSection title="请求趋势" className="overview-chart-section">
          <div className="chart" aria-label="请求趋势图">
            <ResponsiveContainer width="100%" height="100%">
              <AreaChart data={overview.series} margin={{ top: 8, right: 8, left: -18, bottom: 0 }}>
                <CartesianGrid stroke="#dfe5e7" strokeDasharray="3 3" vertical={false} />
                <XAxis
                  dataKey="timestamp"
                  tickFormatter={(value: string) =>
                    new Intl.DateTimeFormat('zh-CN', { hour: '2-digit', minute: '2-digit' }).format(
                      new Date(value),
                    )
                  }
                  tick={{ fontSize: 11, fill: '#65717c' }}
                  axisLine={false}
                  tickLine={false}
                />
                <YAxis tick={{ fontSize: 11, fill: '#65717c' }} axisLine={false} tickLine={false} />
                <Tooltip
                  labelFormatter={(value) => formatDateTime(String(value))}
                  formatter={(value) => [formatNumber(Number(value)), '请求']}
                />
                <Area
                  type="monotone"
                  dataKey="requests"
                  stroke="#0f766e"
                  fill="#99d5cc"
                  fillOpacity={0.38}
                  strokeWidth={2}
                  isAnimationActive={false}
                />
              </AreaChart>
            </ResponsiveContainer>
          </div>
        </PageSection>

        <PageSection title="资源池容量">
          <div className="capacity-list">
            {overview.pools.map((pool) => (
              <div className="capacity-row" key={pool.resourceDomain}>
                <div className="capacity-row__name">
                  <Layers3 size={17} />
                  <strong>{pool.resourceDomain === 'free' ? '免费资源域' : '专业资源域'}</strong>
                </div>
                <dl>
                  <div>
                    <dt>就绪</dt>
                    <dd>{pool.readyCredentials}</dd>
                  </div>
                  <div>
                    <dt>繁忙</dt>
                    <dd>{pool.busyCredentials}</dd>
                  </div>
                  <div>
                    <dt>冷却</dt>
                    <dd>{pool.coolingCredentials}</dd>
                  </div>
                  <div>
                    <dt>排队</dt>
                    <dd>{pool.queuedRequests}</dd>
                  </div>
                </dl>
              </div>
            ))}
          </div>
        </PageSection>
      </div>

      <PageSection title="当前告警">
        {overview.alerts.length > 0 ? (
          <div className="alert-list">
            {overview.alerts.map((alert) => (
              <article className="alert-row" key={alert.id}>
                <AlertTriangle size={18} />
                <div>
                  <div className="alert-row__title">
                    <strong>{alert.title}</strong>
                    <Badge
                      tone={
                        alert.severity === 'critical'
                          ? 'danger'
                          : alert.severity === 'warning'
                            ? 'warning'
                            : 'info'
                      }
                    >
                      {alert.severity === 'critical'
                        ? '严重'
                        : alert.severity === 'warning'
                          ? '警告'
                          : '信息'}
                    </Badge>
                  </div>
                  <p>{alert.summary}</p>
                  <small>
                    {formatDateTime(alert.occurredAt)}
                    {alert.requestId ? ` · ${alert.requestId}` : ''}
                  </small>
                </div>
              </article>
            ))}
          </div>
        ) : (
          <div className="quiet-result">
            <CheckCircle2 size={18} />
            没有待处理告警
          </div>
        )}
      </PageSection>
    </Page>
  )
}

function SummaryMetric({
  icon,
  label,
  value,
}: {
  icon: React.ReactNode
  label: string
  value: string
}) {
  return (
    <article className="summary-metric">
      <div className="summary-metric__label">
        {icon}
        <span>{label}</span>
      </div>
      <strong>{value}</strong>
    </article>
  )
}
