import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Edit3, FlaskConical, Plus, Power } from 'lucide-react'
import { useMemo, useState } from 'react'

import { catalogApi, type OperationSnapshot, type Provider } from '@/api'
import { DataTable, type ColumnDef } from '@/components/data-table/data-table'
import { TableToolbar } from '@/components/data-table/table-toolbar'
import { Page, PageHeader, PageSection } from '@/components/layout'
import { OperationPanel } from '@/components/operations/operation-panel'
import { Badge, StatusBadge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { DialogFrame } from '@/components/ui/dialog'
import { IconButton } from '@/components/ui/icon-button'
import { useSession, hasCapability } from '@/app/session'
import { useListSearch } from '@/hooks/use-list-search'
import { formatDateTime } from '@/lib/format'

import { CatalogTabs } from './catalog-tabs'
import { ProviderForm } from './provider-form'

export function ProvidersPage() {
  const session = useSession()
  const canWrite = hasCapability(session, 'providers:write')
  const { state, setPage, setSearch, setStatus } = useListSearch()
  const queryClient = useQueryClient()
  const [editing, setEditing] = useState<Provider | null | 'create'>(null)
  const [testing, setTesting] = useState<Provider | null>(null)
  const [operation, setOperation] = useState<OperationSnapshot | null>(null)
  const query = useQuery({
    queryKey: ['providers', state],
    queryFn: ({ signal }) => catalogApi.providers(state, signal),
    placeholderData: keepPreviousData,
  })
  const toggle = useMutation({
    mutationFn: (provider: Provider) =>
      catalogApi.setProviderEnabled(provider.id, provider.status !== 'active'),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['providers'] }),
  })
  const test = useMutation({
    mutationFn: ({ provider, mode }: { provider: Provider; mode: 'connection' | 'generation' }) =>
      catalogApi.testProvider(provider.id, mode),
    onSuccess: setOperation,
  })

  const columns = useMemo<ColumnDef<Provider, unknown>[]>(
    () => [
      {
        accessorKey: 'name',
        header: 'Provider',
        cell: ({ row }) => <strong>{row.original.name}</strong>,
      },
      { accessorKey: 'kind', header: '类型' },
      {
        accessorKey: 'resourceDomain',
        header: '资源域',
        cell: ({ row }) => (
          <Badge tone={row.original.resourceDomain === 'free' ? 'positive' : 'info'}>
            {row.original.resourceDomain === 'free' ? '免费' : '专业'}
          </Badge>
        ),
      },
      { accessorKey: 'modelCount', header: '模型' },
      { accessorKey: 'credentialCount', header: '凭据' },
      {
        accessorKey: 'status',
        header: '状态',
        cell: ({ row }) => <StatusBadge status={row.original.status} />,
      },
      {
        accessorKey: 'verifiedAt',
        header: '最近核验',
        cell: ({ row }) => formatDateTime(row.original.verifiedAt),
      },
      {
        id: 'actions',
        header: '操作',
        cell: ({ row }) =>
          canWrite ? (
            <div className="row-actions" onClick={(event) => event.stopPropagation()}>
              <IconButton label="编辑 Provider" onClick={() => setEditing(row.original)}>
                <Edit3 size={16} />
              </IconButton>
              <IconButton
                label="连接测试"
                onClick={() => {
                  setTesting(row.original)
                  setOperation(null)
                }}
              >
                <FlaskConical size={16} />
              </IconButton>
              <IconButton
                label={row.original.status === 'active' ? '停用 Provider' : '启用 Provider'}
                disabled={toggle.isPending}
                onClick={() => toggle.mutate(row.original)}
              >
                <Power size={16} />
              </IconButton>
            </div>
          ) : null,
      },
    ],
    [canWrite, toggle],
  )

  return (
    <Page>
      <PageHeader
        title="Provider 与模型"
        description="上游端点、模型能力与可发布配置"
        actions={
          canWrite ? (
            <Button icon={<Plus size={16} />} onClick={() => setEditing('create')}>
              添加 Provider
            </Button>
          ) : null
        }
      />
      <CatalogTabs />
      <PageSection>
        <TableToolbar
          search={state.search}
          onSearchChange={setSearch}
          searchLabel="搜索 Provider"
          status={state.status}
          onStatusChange={setStatus}
          statusOptions={[
            { value: 'active', label: '可用' },
            { value: 'disabled', label: '已停用' },
            { value: 'unknown', label: '未知' },
          ]}
        />
        <DataTable
          ariaLabel="Provider 列表"
          data={query.data?.items ?? []}
          columns={columns}
          getRowId={(provider) => provider.id}
          loading={query.isLoading}
          fetching={query.isFetching}
          error={query.error}
          onRetry={() => void query.refetch()}
          emptyLabel="没有符合条件的 Provider"
          page={query.data?.page ?? state.page}
          pageSize={query.data?.pageSize ?? state.pageSize}
          total={query.data?.total ?? 0}
          onPageChange={setPage}
          onRowClick={canWrite ? (provider) => setEditing(provider) : undefined}
          renderMobile={(provider) => (
            <div className="mobile-summary">
              <div>
                <strong>{provider.name}</strong>
                <StatusBadge status={provider.status} />
              </div>
              <span>{provider.kind}</span>
              <span>
                {provider.modelCount} 个模型 · {provider.credentialCount} 个凭据
              </span>
            </div>
          )}
        />
      </PageSection>

      <ProviderForm
        open={editing !== null}
        onOpenChange={(open) => {
          if (!open) setEditing(null)
        }}
        {...(editing && editing !== 'create' ? { provider: editing } : {})}
      />

      <DialogFrame
        open={testing !== null}
        onOpenChange={(open) => {
          if (!open) {
            setTesting(null)
            setOperation(null)
          }
        }}
        title={`测试 ${testing?.name ?? ''}`}
        description="连接探测不生成 Token；生成测试可能产生上游用量"
        footer={
          !operation && testing ? (
            <>
              <Button
                variant="secondary"
                disabled={test.isPending}
                onClick={() => test.mutate({ provider: testing, mode: 'connection' })}
              >
                连接探测
              </Button>
              <Button
                disabled={test.isPending}
                onClick={() => test.mutate({ provider: testing, mode: 'generation' })}
              >
                生成测试
              </Button>
            </>
          ) : undefined
        }
      >
        {operation ? (
          <OperationPanel initial={operation} />
        ) : (
          <div className="test-choice">
            <FlaskConical size={24} />
            <span>选择测试方式</span>
          </div>
        )}
      </DialogFrame>
    </Page>
  )
}
