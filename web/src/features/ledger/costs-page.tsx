import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { Plus } from 'lucide-react'
import { useMemo, useState } from 'react'

import { costingApi, type CostSummary, type ModelPriceVersion } from '@/api'
import { DataTable, type ColumnDef } from '@/components/data-table/data-table'
import { TableToolbar } from '@/components/data-table/table-toolbar'
import { Page, PageHeader, PageSection } from '@/components/layout'
import { Button } from '@/components/ui/button'
import { useListSearch } from '@/hooks/use-list-search'
import { formatDateTime, formatNumber } from '@/lib/format'

import { PriceForm } from './price-form'
import { CostBreakdownChart } from './cost-breakdown-chart'

export function CostsPage() {
  const [formOpen, setFormOpen] = useState(false)
  const [pricePage, setPricePage] = useState(1)
  const { state, setPage, setSearch } = useListSearch()
  const prices = useQuery({
    queryKey: ['model-prices', pricePage],
    queryFn: ({ signal }) => costingApi.prices({ page: pricePage, pageSize: 20 }, signal),
  })
  const summaries = useQuery({
    queryKey: ['cost-summaries', state],
    queryFn: ({ signal }) => costingApi.summaries(state, signal),
    placeholderData: keepPreviousData,
  })
  const priceColumns = useMemo<ColumnDef<ModelPriceVersion, unknown>[]>(
    () => [
      {
        accessorKey: 'effectiveAt',
        header: '生效时间',
        cell: ({ row }) => formatDateTime(row.original.effectiveAt),
      },
      { accessorKey: 'modelAlias', header: '模型' },
      { accessorKey: 'currency', header: '币种', meta: { align: 'center' } },
      {
        accessorKey: 'inputPricePerMillionTokens',
        header: '输入 / 百万 Token',
        meta: { align: 'right' },
      },
      {
        accessorKey: 'outputPricePerMillionTokens',
        header: '输出 / 百万 Token',
        meta: { align: 'right' },
      },
    ],
    [],
  )
  const summaryColumns = useMemo<ColumnDef<CostSummary, unknown>[]>(
    () => [
      { accessorKey: 'userName', header: '成员' },
      {
        accessorKey: 'servicePlanName',
        header: '套餐',
        cell: ({ row }) => (
          <div>
            <strong>{row.original.servicePlanName}</strong>
            <small className="table-subline">
              {row.original.planKind === 'coding' ? 'Coding Plan' : 'Token Plan'}
            </small>
          </div>
        ),
      },
      { accessorKey: 'modelAlias', header: '模型' },
      { accessorKey: 'providerName', header: '上游平台' },
      { accessorKey: 'resourcePoolName', header: '资源池' },
      {
        accessorKey: 'requestCount',
        header: '请求',
        cell: ({ row }) => formatNumber(row.original.requestCount),
        meta: { align: 'right' },
      },
      {
        accessorKey: 'inputTokens',
        header: '输入 Token',
        cell: ({ row }) => formatNumber(row.original.inputTokens),
        meta: { align: 'right' },
      },
      {
        accessorKey: 'outputTokens',
        header: '输出 Token',
        cell: ({ row }) => formatNumber(row.original.outputTokens),
        meta: { align: 'right' },
      },
      {
        accessorKey: 'totalCostNanos',
        header: '采购成本',
        cell: ({ row }) => formatMoneyNanos(row.original.totalCostNanos, row.original.currency),
        meta: { align: 'right' },
      },
    ],
    [],
  )
  return (
    <Page>
      <PageHeader
        title="上游成本"
        actions={
          <Button
            icon={<Plus size={16} />}
            data-onboarding="create-price"
            onClick={() => setFormOpen(true)}
          >
            新增价格
          </Button>
        }
      />
      <PageSection title="价格版本">
        <DataTable
          ariaLabel="模型价格版本"
          data={prices.data?.items ?? []}
          columns={priceColumns}
          getRowId={(item) => item.id}
          loading={prices.isLoading}
          fetching={prices.isFetching}
          error={prices.error}
          onRetry={() => void prices.refetch()}
          emptyLabel="尚未配置模型价格"
          page={prices.data?.page ?? pricePage}
          pageSize={prices.data?.pageSize ?? 20}
          total={prices.data?.total ?? 0}
          onPageChange={setPricePage}
        />
      </PageSection>
      <PageSection title="采购成本汇总">
        <TableToolbar
          search={state.search}
          onSearchChange={setSearch}
          searchLabel="搜索成员、模型、上游平台或币种"
        />
        {summaries.data?.items.length ? <CostBreakdownChart items={summaries.data.items} /> : null}
        <DataTable
          ariaLabel="采购成本汇总"
          data={summaries.data?.items ?? []}
          columns={summaryColumns}
          getRowId={(item) =>
            `${item.userId}:${item.subscriptionId}:${item.modelId}:${item.resourcePoolId}:${item.currency}`
          }
          loading={summaries.isLoading}
          fetching={summaries.isFetching}
          error={summaries.error}
          onRetry={() => void summaries.refetch()}
          emptyLabel="尚无已结算成本"
          page={summaries.data?.page ?? state.page}
          pageSize={summaries.data?.pageSize ?? state.pageSize}
          total={summaries.data?.total ?? 0}
          onPageChange={setPage}
        />
      </PageSection>
      <PriceForm open={formOpen} onOpenChange={setFormOpen} />
    </Page>
  )
}

function formatMoneyNanos(value: string, currency: string): string {
  const nanos = BigInt(value)
  const whole = nanos / 1_000_000_000n
  const fraction = (nanos % 1_000_000_000n).toString().padStart(9, '0').replace(/0+$/, '')
  return `${currency} ${whole.toString()}${fraction ? `.${fraction}` : ''}`
}
