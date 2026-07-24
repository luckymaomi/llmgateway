import {
  Bar,
  BarChart,
  CartesianGrid,
  Cell,
  ComposedChart,
  Legend,
  Line,
  Pie,
  PieChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'

import type { OperationsOverview } from '@/api'
import { formatDateTime, formatNumber, formatTokens } from '@/lib/format'

const chartColors = {
  positive: 'var(--positive)',
  accent: 'var(--accent)',
  blue: 'var(--blue)',
  warning: 'var(--warning)',
  danger: 'var(--danger)',
  muted: 'var(--border-strong)',
} as const

export function RequestTrendChart({ overview }: { overview: OperationsOverview }) {
  const data = overview.trend.map((point) => ({
    ...point,
    tokens: point.inputTokens + point.outputTokens,
  }))
  return (
    <div className="chart chart--trend" role="img" aria-label="24 小时请求与 Token 趋势">
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
          <Legend
            verticalAlign="top"
            align="right"
            formatter={(value) => (value === 'tokens' ? 'Token' : '请求')}
          />
          <Bar
            yAxisId="requests"
            dataKey="requestCount"
            fill={chartColors.accent}
            radius={[3, 3, 0, 0]}
            maxBarSize={18}
          />
          <Line
            yAxisId="tokens"
            type="monotone"
            dataKey="tokens"
            stroke={chartColors.blue}
            strokeWidth={2}
            dot={false}
          />
        </ComposedChart>
      </ResponsiveContainer>
    </div>
  )
}

export function RequestOutcomeChart({ overview }: { overview: OperationsOverview }) {
  const requests = overview.requests
  const processing = Math.max(
    0,
    requests.requestCount -
      requests.completedCount -
      requests.failedCount -
      requests.uncertainCount,
  )
  return (
    <DistributionChart
      total={requests.requestCount}
      totalLabel="请求"
      emptyLabel="24 小时内没有请求"
      ariaLabel="24 小时请求终态分布"
      data={[
        { name: '已完成', value: requests.completedCount, color: chartColors.positive },
        { name: '失败或取消', value: requests.failedCount, color: chartColors.danger },
        { name: '结果未知', value: requests.uncertainCount, color: chartColors.warning },
        { name: '处理中', value: processing, color: chartColors.blue },
      ]}
    />
  )
}

export function CredentialStatusChart({
  overview,
}: {
  overview: Extract<OperationsOverview, { scope: 'administrator' }>
}) {
  const resources = overview.resources
  const inactive = Math.max(
    0,
    resources.credentialCount - resources.activeCredentialCount - resources.coolingCredentialCount,
  )
  return (
    <DistributionChart
      total={resources.credentialCount}
      totalLabel="上游 API Key"
      emptyLabel="尚未添加上游 API Key"
      ariaLabel="上游 API Key 状态分布"
      data={[
        { name: '可用', value: resources.activeCredentialCount, color: chartColors.positive },
        { name: '冷却中', value: resources.coolingCredentialCount, color: chartColors.warning },
        { name: '停用或不可用', value: inactive, color: chartColors.muted },
      ]}
    />
  )
}

export function RequestErrorChart({ overview }: { overview: OperationsOverview }) {
  const data = overview.errors.length > 0 ? overview.errors : [{ kind: '无错误', count: 0 }]
  const height = Math.max(190, data.length * 38)
  return (
    <div className="chart chart--errors" style={{ height }} role="img" aria-label="请求错误分布">
      <ResponsiveContainer width="100%" height="100%">
        <BarChart data={data} layout="vertical" margin={{ top: 4, right: 24, bottom: 0, left: 4 }}>
          <CartesianGrid horizontal={false} stroke="var(--border)" />
          <XAxis type="number" allowDecimals={false} stroke="var(--muted)" fontSize={11} />
          <YAxis
            type="category"
            dataKey="kind"
            width={138}
            stroke="var(--muted)"
            fontSize={10}
            tickLine={false}
          />
          <Tooltip formatter={(value) => [formatNumber(Number(value)), '请求']} />
          <Bar dataKey="count" fill={chartColors.danger} radius={[0, 3, 3, 0]} maxBarSize={18} />
        </BarChart>
      </ResponsiveContainer>
    </div>
  )
}

type DistributionDatum = { name: string; value: number; color: string }

function DistributionChart({
  total,
  totalLabel,
  emptyLabel,
  ariaLabel,
  data,
}: {
  total: number
  totalLabel: string
  emptyLabel: string
  ariaLabel: string
  data: DistributionDatum[]
}) {
  const visible =
    total === 0
      ? [{ name: emptyLabel, value: 1, color: chartColors.muted }]
      : data.filter((item) => item.value > 0)
  return (
    <div className="distribution" role="img" aria-label={ariaLabel}>
      <div className="distribution__plot">
        <ResponsiveContainer width="100%" height="100%">
          <PieChart>
            <Pie
              data={visible}
              dataKey="value"
              nameKey="name"
              innerRadius="64%"
              outerRadius="88%"
              paddingAngle={2}
              stroke="var(--surface)"
              strokeWidth={2}
            >
              {visible.map((item) => (
                <Cell key={item.name} fill={item.color} />
              ))}
            </Pie>
            {total > 0 ? (
              <Tooltip formatter={(value) => [formatNumber(Number(value)), '数量']} />
            ) : null}
          </PieChart>
        </ResponsiveContainer>
        <div className="distribution__total" aria-hidden="true">
          <strong>{formatNumber(total)}</strong>
          <span>{totalLabel}</span>
        </div>
      </div>
      <div className="distribution__legend">
        {data.map((item) => (
          <div key={item.name}>
            <i style={{ backgroundColor: item.color }} />
            <span>{item.name}</span>
            <strong>{formatNumber(item.value)}</strong>
          </div>
        ))}
      </div>
    </div>
  )
}
