import {
  Bar,
  BarChart,
  CartesianGrid,
  Legend,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'

import type { Entitlement } from '@/api'
import { formatTokens } from '@/lib/format'

export function EntitlementUtilizationChart({ items }: { items: Entitlement[] }) {
  const data = items
    .filter((item) => item.grantedTokens > 0)
    .slice(0, 10)
    .map((item) => {
      const remaining = Math.max(0, Math.min(item.balanceTokens, item.grantedTokens))
      return {
        id: item.id,
        label: `${item.ownerName} · ${item.modelAlias ?? (item.resourceDomain === 'free' ? '免费域' : '专业域')}`,
        used: item.grantedTokens - remaining,
        remaining,
      }
    })
  if (data.length === 0) return null
  return (
    <div
      className="chart chart--quota"
      style={{ height: Math.max(220, data.length * 42) }}
      role="img"
      aria-label="当前页额度消耗"
    >
      <ResponsiveContainer width="100%" height="100%">
        <BarChart data={data} layout="vertical" margin={{ top: 8, right: 16, bottom: 0, left: 4 }}>
          <CartesianGrid horizontal={false} stroke="var(--border)" />
          <XAxis
            type="number"
            tickFormatter={(value: number) => formatTokens(value)}
            stroke="var(--muted)"
            fontSize={11}
          />
          <YAxis
            type="category"
            dataKey="label"
            width={150}
            stroke="var(--muted)"
            fontSize={10}
            tickLine={false}
          />
          <Tooltip formatter={(value, name) => [formatTokens(Number(value)), name]} />
          <Legend verticalAlign="top" align="right" />
          <Bar
            name="已使用"
            dataKey="used"
            stackId="quota"
            fill="var(--accent)"
            radius={[3, 0, 0, 3]}
            maxBarSize={18}
          />
          <Bar
            name="剩余"
            dataKey="remaining"
            stackId="quota"
            fill="var(--blue)"
            radius={[0, 3, 3, 0]}
            maxBarSize={18}
          />
        </BarChart>
      </ResponsiveContainer>
    </div>
  )
}
