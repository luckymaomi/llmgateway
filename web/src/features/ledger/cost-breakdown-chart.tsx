import { Bar, BarChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'

import type { CostSummary } from '@/api'

export function CostBreakdownChart({ items }: { items: CostSummary[] }) {
  const byCurrency = new Map<string, Map<string, bigint>>()
  for (const item of items) {
    const byModel = byCurrency.get(item.currency) ?? new Map<string, bigint>()
    byModel.set(item.modelAlias, (byModel.get(item.modelAlias) ?? 0n) + BigInt(item.totalCostNanos))
    byCurrency.set(item.currency, byModel)
  }
  if (byCurrency.size === 0) return null
  return (
    <div className="cost-charts" aria-label="当前筛选页采购成本构成">
      {[...byCurrency.entries()].map(([currency, byModel]) => {
        const data = [...byModel.entries()]
          .sort((left, right) => (left[1] === right[1] ? 0 : left[1] > right[1] ? -1 : 1))
          .slice(0, 10)
          .map(([model, nanos]) => ({ model, cost: Number(nanos) / 1_000_000_000 }))
        return (
          <div className="cost-chart" key={currency}>
            <h3>{currency}</h3>
            <div className="chart" style={{ height: Math.max(190, data.length * 38) }}>
              <ResponsiveContainer width="100%" height="100%">
                <BarChart
                  data={data}
                  layout="vertical"
                  margin={{ top: 4, right: 22, bottom: 0, left: 4 }}
                >
                  <CartesianGrid horizontal={false} stroke="var(--border)" />
                  <XAxis
                    type="number"
                    tickFormatter={(value: number) => formatChartMoney(value)}
                    stroke="var(--muted)"
                    fontSize={11}
                  />
                  <YAxis
                    type="category"
                    dataKey="model"
                    width={120}
                    stroke="var(--muted)"
                    fontSize={10}
                    tickLine={false}
                  />
                  <Tooltip
                    formatter={(value) => [
                      `${currency} ${formatChartMoney(Number(value))}`,
                      '采购成本',
                    ]}
                  />
                  <Bar dataKey="cost" fill="var(--accent)" radius={[0, 3, 3, 0]} maxBarSize={18} />
                </BarChart>
              </ResponsiveContainer>
            </div>
          </div>
        )
      })}
    </div>
  )
}

function formatChartMoney(value: number): string {
  return new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 4 }).format(value)
}
