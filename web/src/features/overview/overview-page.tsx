import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { Activity, Check, Circle, KeyRound, Network, UsersRound, Zap } from 'lucide-react'
import {
  Bar,
  CartesianGrid,
  ComposedChart,
  Line,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'

import { operationsApi, type OperationsOverview, type OverviewStep } from '@/api'
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

export function OverviewPage() {
  const query = useQuery({
    queryKey: ['operations-overview'],
    queryFn: ({ signal }) => operationsApi.overview(signal),
    refetchInterval: 60_000,
  })
  if (query.isLoading)
    return (
      <Page>
        <LoadingState label="正在读取运行状态" />
      </Page>
    )
  if (query.error || !query.data)
    return (
      <Page>
        <ErrorState error={query.error} onRetry={() => void query.refetch()} />
      </Page>
    )
  return query.data.scope === 'administrator' ? (
    <AdministratorOverviewView overview={query.data} />
  ) : (
    <MemberOverviewView overview={query.data} />
  )
}

function AdministratorOverviewView({
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
      <PageHeader title="总览" />
      <div className="summary-grid">
        <SummaryMetric
          icon={<Activity size={16} />}
          label="24 小时请求"
          value={formatNumber(overview.requests.requestCount)}
        />
        <SummaryMetric icon={<Zap size={16} />} label="成功率" value={formatPercent(successRate)} />
        <SummaryMetric
          icon={<Network size={16} />}
          label="Provider API Key"
          value={`${overview.resources.activeCredentialCount} / ${overview.resources.credentialCount}`}
        />
        <SummaryMetric
          icon={<UsersRound size={16} />}
          label="成员"
          value={formatNumber(overview.resources.activeMemberCount)}
          marker={
            overview.resources.pendingMemberCount > 0
              ? `${overview.resources.pendingMemberCount} 待审核`
              : undefined
          }
        />
      </div>
      <NextActions scope="administrator" steps={overview.steps} />
      <PageSection title="24 小时趋势">
        <OverviewChart overview={overview} />
      </PageSection>
      <div className="overview-grid">
        <PageSection title="运行状态">
          <dl className="fact-grid">
            <Fact
              label="Provider"
              value={`${overview.resources.enabledProviderCount} / ${overview.resources.providerCount}`}
            />
            <Fact label="模型" value={formatNumber(overview.resources.modelCount)} />
            <Fact label="首字节 P95" value={formatDuration(overview.requests.firstByteP95Ms)} />
            <Fact label="总延迟 P95" value={formatDuration(overview.requests.totalLatencyP95Ms)} />
          </dl>
        </PageSection>
        <ErrorDistribution overview={overview} />
      </div>
    </Page>
  )
}

function MemberOverviewView({
  overview,
}: {
  overview: Extract<OperationsOverview, { scope: 'member' }>
}) {
  return (
    <Page>
      <PageHeader title="总览" />
      <div className="summary-grid">
        <SummaryMetric
          icon={<Zap size={16} />}
          label="剩余 Token"
          value={formatTokens(overview.access.remainingTokens)}
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
        <SummaryMetric
          icon={<KeyRound size={16} />}
          label="API Key"
          value={formatNumber(overview.access.activeGatewayKeyCount)}
          marker={
            overview.access.nearestEntitlementExpiry
              ? `额度到期 ${formatDateTime(overview.access.nearestEntitlementExpiry)}`
              : undefined
          }
        />
      </div>
      <NextActions scope="member" steps={overview.steps} />
      <PageSection title="24 小时趋势">
        <OverviewChart overview={overview} />
      </PageSection>
      <div className="overview-grid">
        <PageSection title="请求状态">
          <dl className="fact-grid">
            <Fact label="完成" value={formatNumber(overview.requests.completedCount)} />
            <Fact label="失败" value={formatNumber(overview.requests.failedCount)} />
            <Fact label="首字节 P95" value={formatDuration(overview.requests.firstByteP95Ms)} />
            <Fact label="总延迟 P95" value={formatDuration(overview.requests.totalLatencyP95Ms)} />
          </dl>
        </PageSection>
        <ErrorDistribution overview={overview} />
      </div>
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

function NextActions({
  scope,
  steps,
}: {
  scope: OperationsOverview['scope']
  steps: OverviewStep[]
}) {
  const pending = steps.filter((step) => !step.complete)
  if (pending.length === 0)
    return (
      <div className="quick-actions" aria-label="常用操作">
        {(scope === 'administrator' ? administratorQuickActions : memberQuickActions).map(
          (action) => (
            <Button key={action.to} asChild variant="secondary" size="sm">
              <Link to={action.to}>{action.label}</Link>
            </Button>
          ),
        )}
      </div>
    )
  return (
    <PageSection title="下一步">
      <div className="action-list" role="list">
        {steps.map((step) => {
          const action = stepActions[scope][step.id]
          if (!action) return null
          return (
            <div className="action-row" role="listitem" key={step.id} data-complete={step.complete}>
              <span className="action-row__state" aria-hidden="true">
                {step.complete ? <Check size={16} /> : <Circle size={16} />}
              </span>
              <strong>{action.label}</strong>
              {!step.complete && action.to ? (
                <Button asChild size="sm">
                  <Link to={action.to}>{action.command}</Link>
                </Button>
              ) : null}
            </div>
          )
        })}
      </div>
    </PageSection>
  )
}

function OverviewChart({ overview }: { overview: OperationsOverview }) {
  const data = overview.trend.map((point) => ({
    ...point,
    tokens: point.inputTokens + point.outputTokens,
  }))
  return (
    <div className="overview-chart" role="img" aria-label="请求与 Token 趋势">
      <ResponsiveContainer width="100%" height="100%">
        <ComposedChart data={data} margin={{ top: 8, right: 8, bottom: 0, left: 0 }}>
          <CartesianGrid vertical={false} stroke="var(--border)" />
          <XAxis
            dataKey="bucket"
            tickFormatter={(value: string) =>
              new Date(value).toLocaleTimeString('zh-CN', { hour: '2-digit' })
            }
            stroke="var(--muted)"
            fontSize={11}
          />
          <YAxis
            yAxisId="requests"
            width={36}
            stroke="var(--muted)"
            fontSize={11}
            allowDecimals={false}
          />
          <YAxis
            yAxisId="tokens"
            orientation="right"
            width={46}
            tickFormatter={(value: number) => formatTokens(value)}
            stroke="var(--muted)"
            fontSize={11}
          />
          <Tooltip
            labelFormatter={(value) => formatDateTime(String(value))}
            formatter={(value, name) => [
              name === 'tokens' ? formatTokens(Number(value)) : formatNumber(Number(value)),
              name === 'tokens' ? 'Token' : '请求',
            ]}
          />
          <Bar
            yAxisId="requests"
            dataKey="requestCount"
            fill="var(--accent)"
            radius={[3, 3, 0, 0]}
            maxBarSize={18}
          />
          <Line
            yAxisId="tokens"
            type="monotone"
            dataKey="tokens"
            stroke="var(--blue)"
            strokeWidth={2}
            dot={false}
          />
        </ComposedChart>
      </ResponsiveContainer>
    </div>
  )
}

function ErrorDistribution({ overview }: { overview: OperationsOverview }) {
  return (
    <PageSection title="错误分布">
      {overview.errors.length === 0 ? (
        <div className="quiet-result">
          <Check size={18} />
          <span>24 小时内无失败请求</span>
        </div>
      ) : (
        <div className="error-distribution">
          {overview.errors.map((item) => (
            <div key={item.kind}>
              <span>{item.kind}</span>
              <strong>{formatNumber(item.count)}</strong>
            </div>
          ))}
        </div>
      )}
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

type StepAction = {
  label: string
  command?: string
  to?:
    | '/providers/providers'
    | '/credentials'
    | '/ledger/costs'
    | '/providers/revisions'
    | '/access/users'
    | '/ledger/entitlements'
    | '/access/keys'
}

const stepActions: Record<OperationsOverview['scope'], Record<string, StepAction>> = {
  administrator: {
    provider: { label: '接入 Provider', command: '接入', to: '/providers/providers' },
    credential: { label: '添加 Provider API Key', command: '添加', to: '/credentials' },
    price: { label: '设置模型价格', command: '设置', to: '/ledger/costs' },
    publication: { label: '发布模型配置', command: '发布', to: '/providers/revisions' },
    member: { label: '激活成员', command: '管理', to: '/access/users' },
    entitlement: { label: '分配成员额度', command: '分配', to: '/ledger/entitlements' },
    gateway_key: { label: '创建 Gateway API Key', command: '创建', to: '/access/keys' },
    request: { label: '测试 Gateway Key', command: '测试', to: '/access/keys' },
  },
  member: {
    entitlement: { label: '额度待管理员分配' },
    gateway_key: { label: '等待管理员分配 API Key' },
    request: { label: '测试 Gateway Key', command: '测试', to: '/access/keys' },
  },
}

const administratorQuickActions = [
  { label: 'Provider API Key', to: '/credentials' },
  { label: '成员', to: '/access/users' },
  { label: '用量', to: '/ledger/usage' },
  { label: 'API Key', to: '/access/keys' },
] as const

const memberQuickActions = [
  { label: 'API Key', to: '/access/keys' },
  { label: '用量', to: '/ledger/usage' },
  { label: 'API Key', to: '/access/keys' },
] as const
