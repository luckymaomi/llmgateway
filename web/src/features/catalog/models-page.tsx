import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { Edit3, Plus } from 'lucide-react'
import { useMemo, useState } from 'react'

import { catalogApi, type Model } from '@/api'
import { hasCapability, useSession } from '@/app/session'
import { DataTable, type ColumnDef } from '@/components/data-table/data-table'
import { TableToolbar } from '@/components/data-table/table-toolbar'
import { Page, PageHeader, PageSection } from '@/components/layout'
import { Badge, StatusBadge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { useListSearch } from '@/hooks/use-list-search'
import { formatNumber } from '@/lib/format'

import { CatalogTabs } from './catalog-tabs'
import { ModelForm } from './model-form'

export function ModelsPage() {
  const session = useSession()
  const canWrite = hasCapability(session, 'providers:write')
  const { state, setPage, setSearch, setStatus } = useListSearch()
  const [editing, setEditing] = useState<Model | null | 'create'>(null)
  const query = useQuery({
    queryKey: ['models', state],
    queryFn: ({ signal }) => catalogApi.models(state, signal),
    placeholderData: keepPreviousData,
  })
  const columns = useMemo<ColumnDef<Model, unknown>[]>(
    () => [
      {
        accessorKey: 'alias',
        header: '网关别名',
        cell: ({ row }) => <strong>{row.original.alias}</strong>,
      },
      { accessorKey: 'upstreamModelId', header: '上游模型 ID' },
      { accessorKey: 'providerName', header: 'Provider' },
      {
        accessorKey: 'capabilities',
        header: '能力',
        cell: ({ row }) => (
          <div className="badge-list">
            {row.original.capabilities.map((value) => (
              <Badge key={value}>{capabilityLabel[value]}</Badge>
            ))}
          </div>
        ),
      },
      {
        accessorKey: 'contextTokens',
        header: '上下文',
        cell: ({ row }) =>
          row.original.contextTokens ? formatNumber(row.original.contextTokens) : '未知',
      },
      {
        accessorKey: 'status',
        header: '状态',
        cell: ({ row }) => <StatusBadge status={row.original.status} />,
      },
      {
        id: 'actions',
        header: '操作',
        cell: ({ row }) =>
          canWrite ? (
            <div className="row-actions" onClick={(event) => event.stopPropagation()}>
              <IconButton label="编辑模型" onClick={() => setEditing(row.original)}>
                <Edit3 size={16} />
              </IconButton>
            </div>
          ) : null,
      },
    ],
    [canWrite],
  )

  return (
    <Page>
      <PageHeader
        title="Provider 接入"
        actions={
          canWrite ? (
            <Button icon={<Plus size={16} />} onClick={() => setEditing('create')}>
              添加模型
            </Button>
          ) : null
        }
      />
      <CatalogTabs />
      <PageSection>
        <TableToolbar
          search={state.search}
          onSearchChange={setSearch}
          searchLabel="搜索模型"
          status={state.status}
          onStatusChange={setStatus}
          statusOptions={[
            { value: 'active', label: '可用' },
            { value: 'disabled', label: '已停用' },
          ]}
        />
        <DataTable
          ariaLabel="模型列表"
          data={query.data?.items ?? []}
          columns={columns}
          getRowId={(model) => model.id}
          loading={query.isLoading}
          fetching={query.isFetching}
          error={query.error}
          onRetry={() => void query.refetch()}
          emptyLabel="没有符合条件的模型"
          page={query.data?.page ?? state.page}
          pageSize={query.data?.pageSize ?? state.pageSize}
          total={query.data?.total ?? 0}
          onPageChange={setPage}
          onRowClick={canWrite ? (model) => setEditing(model) : undefined}
          renderMobile={(model) => (
            <div className="mobile-summary">
              <div>
                <strong>{model.alias}</strong>
                <StatusBadge status={model.status} />
              </div>
              <span>
                {model.providerName} · {model.upstreamModelId}
              </span>
              <div className="badge-list">
                {model.capabilities.map((value) => (
                  <Badge key={value}>{capabilityLabel[value]}</Badge>
                ))}
              </div>
            </div>
          )}
        />
      </PageSection>
      <ModelForm
        open={editing !== null}
        onOpenChange={(open) => {
          if (!open) setEditing(null)
        }}
        {...(editing && editing !== 'create' ? { model: editing } : {})}
      />
    </Page>
  )
}

const capabilityLabel = {
  streaming: '流式',
  tools: '工具',
  reasoning: '推理',
  structured_output: '结构化',
} as const
