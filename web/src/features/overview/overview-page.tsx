import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { Activity, KeyRound, Network, PackageCheck, UsersRound, Zap } from 'lucide-react'

import { operationsApi, type OperationsOverview } from '@/api'
import { Page, PageHeader, PageSection } from '@/components/layout'
import { Button } from '@/components/ui/button'
import { ErrorState, LoadingState } from '@/components/ui/state'
import {
  formatDateTime,
  formatDuration,
  formatNumber,
  formatPercent,
  formatTokens,
} from '@/lib/format'

import { RequestErrorChart, RequestOutcomeChart, RequestTrendChart } from './operations-charts'

export function OverviewPage() {
  const query = useQuery({
    queryKey: ['operations-overview'],
    queryFn: ({ signal }) => operationsApi.overview(signal),
    refetchInterval: 60_000,
  })
  if (query.isLoading)
    return (
      <Page>
        <LoadingState label="正在读取仪表盘" />
      </Page>
    )
  if (query.error || !query.data)
    return (
      <Page>
        <ErrorState error={query.error} onRetry={() => void query.refetch()} />
      </Page>
    )
  return query.data.scope === 'administrator' ? (
    <AdministratorView overview={query.data} />
  ) : (
    <MemberView overview={query.data} />
  )
}

function AdministratorView({
  overview,
}: {
  overview: Extract<OperationsOverview, { scope: 'administrator' }>
}) {
  const successRate =
    overview.requests.requestCount > 0
      ? overview.requests.completedCount / overview.requests.requestCount
      : undefined
  return (
    <Page>
      <PageHeader
        title="仪表盘"
        actions={
          !overview.resources.hasActiveUpstream ? (
            <Button asChild>
              <Link to="/provider-keys">处理上游资源</Link>
            </Button>
          ) : undefined
        }
      />
      <div className="summary-grid">
        <SummaryMetric
          icon={<Activity size={16} />}
          label="24 小时请求"
          value={formatNumber(overview.requests.requestCount)}
        />
        <SummaryMetric icon={<Zap size={16} />} label="完成率" value={formatPercent(successRate)} />
        <SummaryMetric
          icon={<Network size={16} />}
          label="活动资源池"
          value={`${overview.resources.activeResourcePoolCount} / ${overview.resources.resourcePoolCount}`}
        />
        <SummaryMetric
          icon={<KeyRound size={16} />}
          label="可用上游 API Key"
          value={`${overview.resources.activeCredentialCount} / ${overview.resources.credentialCount}`}
        />
        <SummaryMetric
          icon={<PackageCheck size={16} />}
          label="活动订阅"
          value={formatNumber(overview.resources.activeSubscriptionCount)}
        />
        <SummaryMetric
          icon={<UsersRound size={16} />}
          label="活动成员"
          value={formatNumber(overview.resources.activeMemberCount)}
        />
      </div>
      <PageSection title="24 小时趋势">
        <RequestTrendChart overview={overview} />
      </PageSection>
      <div className="chart-grid">
        <PageSection title="请求终态">
          <RequestOutcomeChart overview={overview} />
        </PageSection>
        <ErrorDistribution overview={overview} />
      </div>
      <PageSection title="运行事实">
        <dl className="fact-grid">
          <Fact
            label="已接入平台"
            value={formatNumber(overview.resources.connectedProviderCount)}
          />
          <Fact label="模型" value={formatNumber(overview.resources.modelCount)} />
          <Fact label="首字节 P95" value={formatDuration(overview.requests.firstByteP95Ms)} />
          <Fact label="总延迟 P95" value={formatDuration(overview.requests.totalLatencyP95Ms)} />
        </dl>
      </PageSection>
    </Page>
  )
}

function MemberView({ overview }: { overview: Extract<OperationsOverview, { scope: 'member' }> }) {
  const available =
    overview.access.activeSubscriptionCount > 0 &&
    overview.access.activeApiKeyCount > 0 &&
    overview.access.remainingTokens > 0
  return (
    <Page>
      <PageHeader
        title="仪表盘"
        actions={
          <Button asChild variant="secondary">
            <Link
              to={
                available
                  ? '/api-logs'
                  : overview.access.activeSubscriptionCount > 0
                    ? '/api-keys'
                    : '/subscriptions'
              }
            >
              {available
                ? '查看 API 日志'
                : overview.access.activeSubscriptionCount > 0
                  ? '管理 API 密钥'
                  : '查看我的订阅'}
            </Link>
          </Button>
        }
      />
      <div className="summary-grid">
        <SummaryMetric
          icon={<Zap size={16} />}
          label="服务状态"
          value={available ? '可用' : '不可用'}
        />
        <SummaryMetric
          icon={<PackageCheck size={16} />}
          label="活动订阅"
          value={formatNumber(overview.access.activeSubscriptionCount)}
          marker={
            overview.access.nearestSubscriptionExpiry
              ? `到期 ${formatDateTime(overview.access.nearestSubscriptionExpiry)}`
              : undefined
          }
        />
        <SummaryMetric
          icon={<Zap size={16} />}
          label="剩余 Token"
          value={formatTokens(overview.access.remainingTokens)}
        />
        <SummaryMetric
          icon={<KeyRound size={16} />}
          label="API 密钥"
          value={formatNumber(overview.access.activeApiKeyCount)}
        />
        <SummaryMetric
          icon={<Activity size={16} />}
          label="24 小时请求"
          value={formatNumber(overview.requests.requestCount)}
        />
        <SummaryMetric
          icon={<Network size={16} />}
          label="24 小时 Token"
          value={formatTokens(overview.requests.inputTokens + overview.requests.outputTokens)}
        />
      </div>
      <PageSection title="24 小时趋势">
        <RequestTrendChart overview={overview} />
      </PageSection>
      <div className="chart-grid">
        <PageSection title="请求终态">
          <RequestOutcomeChart overview={overview} />
        </PageSection>
        <ErrorDistribution overview={overview} />
      </div>
      <PageSection title="请求状态">
        <dl className="fact-grid">
          <Fact label="完成" value={formatNumber(overview.requests.completedCount)} />
          <Fact label="失败" value={formatNumber(overview.requests.failedCount)} />
          <Fact label="首字节 P95" value={formatDuration(overview.requests.firstByteP95Ms)} />
          <Fact label="总延迟 P95" value={formatDuration(overview.requests.totalLatencyP95Ms)} />
        </dl>
      </PageSection>
    </Page>
  )
}

function SummaryMetric({
  icon,
  label,
  value,
  marker,
}: {
  icon: React.ReactNode
  label: string
  value: string
  marker?: string | undefined
}) {
  return (
    <div className="summary-metric">
      <span className="summary-metric__label">
        {icon}
        {label}
      </span>
      <strong>{value}</strong>
      {marker ? <small className="summary-metric__marker">{marker}</small> : null}
    </div>
  )
}

function ErrorDistribution({ overview }: { overview: OperationsOverview }) {
  return (
    <PageSection title="错误分布">
      <RequestErrorChart overview={overview} />
    </PageSection>
  )
}
function Fact({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <dt>{label}</dt>
      <dd>{value}</dd>
    </div>
  )
}
