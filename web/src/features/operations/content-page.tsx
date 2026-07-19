import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Eye, Trash2 } from 'lucide-react'
import { useMemo, useState } from 'react'

import { operationsApi, type ContentRecord, type OperationSnapshot } from '@/api'
import { DataTable, type ColumnDef } from '@/components/data-table/data-table'
import { TableToolbar } from '@/components/data-table/table-toolbar'
import { Page, PageHeader, PageSection } from '@/components/layout'
import { OperationPanel } from '@/components/operations/operation-panel'
import { StatusBadge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { DetailDrawer } from '@/components/ui/detail-drawer'
import { DialogFrame } from '@/components/ui/dialog'
import { Field, Textarea } from '@/components/ui/field'
import { FormProblem } from '@/features/auth/form-problem'
import { useListSearch } from '@/hooks/use-list-search'
import { formatDateTime } from '@/lib/format'

import { OperationsTabs } from './operations-tabs'

export function ContentPage() {
  const { state, setPage, setSearch, setStatus } = useListSearch()
  const queryClient = useQueryClient()
  const [accessing, setAccessing] = useState<ContentRecord | null>(null)
  const [reason, setReason] = useState('')
  const [revealed, setRevealed] = useState<ContentRecord | null>(null)
  const [operation, setOperation] = useState<OperationSnapshot | null>(null)
  const query = useQuery({
    queryKey: ['content-records', state],
    queryFn: ({ signal }) => operationsApi.contentRecords(state, signal),
    placeholderData: keepPreviousData,
  })
  const access = useMutation({
    mutationFn: ({ id, reason: accessReason }: { id: string; reason: string }) =>
      operationsApi.revealContent(id, accessReason),
    onSuccess(record) {
      setRevealed(record)
      setAccessing(null)
      setReason('')
    },
  })
  const remove = useMutation({
    mutationFn: operationsApi.scheduleContentDeletion,
    onSuccess(snapshot) {
      setOperation(snapshot)
      void queryClient.invalidateQueries({ queryKey: ['content-records'] })
    },
  })
  const columns = useMemo<ColumnDef<ContentRecord, unknown>[]>(
    () => [
      {
        accessorKey: 'capturedAt',
        header: '留存时间',
        cell: ({ row }) => formatDateTime(row.original.capturedAt),
      },
      {
        accessorKey: 'requestId',
        header: 'Request ID',
        cell: ({ row }) => <code>{row.original.requestId}</code>,
      },
      { accessorKey: 'ownerName', header: '用户' },
      { accessorKey: 'modelAlias', header: '模型' },
      {
        accessorKey: 'expiresAt',
        header: '删除时间',
        cell: ({ row }) => formatDateTime(row.original.expiresAt),
      },
      {
        accessorKey: 'status',
        header: '状态',
        cell: ({ row }) => <StatusBadge status={row.original.status} />,
      },
      {
        id: 'actions',
        header: '操作',
        cell: ({ row }) => (
          <div className="row-actions">
            <Button
              size="sm"
              variant="quiet"
              icon={<Eye size={15} />}
              onClick={() => setAccessing(row.original)}
            >
              受控查看
            </Button>
            <Button
              size="sm"
              variant="quiet"
              icon={<Trash2 size={15} />}
              disabled={remove.isPending || row.original.status !== 'retained'}
              onClick={() => remove.mutate(row.original.id)}
            >
              安排删除
            </Button>
          </div>
        ),
      },
    ],
    [remove],
  )
  return (
    <Page>
      <PageHeader title="请求与审计" description="请求、路由 attempt、错误与管理操作" />
      <OperationsTabs />
      <PageSection>
        <TableToolbar
          search={state.search}
          onSearchChange={setSearch}
          searchLabel="搜索 Request ID、用户或模型"
          status={state.status}
          onStatusChange={setStatus}
          statusOptions={[
            { value: 'retained', label: '留存中' },
            { value: 'deletion_scheduled', label: '待删除' },
            { value: 'deleted', label: '已删除' },
          ]}
        />
        <DataTable
          ariaLabel="内容留存列表"
          data={query.data?.items ?? []}
          columns={columns}
          getRowId={(record) => record.id}
          loading={query.isLoading}
          fetching={query.isFetching}
          error={query.error ?? remove.error}
          onRetry={() => void query.refetch()}
          emptyLabel="没有符合条件的内容记录"
          page={query.data?.page ?? state.page}
          pageSize={query.data?.pageSize ?? state.pageSize}
          total={query.data?.total ?? 0}
          onPageChange={setPage}
          renderMobile={(record) => (
            <div className="mobile-summary">
              <div>
                <code>{record.requestId}</code>
                <StatusBadge status={record.status} />
              </div>
              <span>
                {record.ownerName} · {record.modelAlias}
              </span>
              <span>{formatDateTime(record.capturedAt)}</span>
            </div>
          )}
        />
      </PageSection>
      <DialogFrame
        open={accessing !== null}
        onOpenChange={(open) => {
          if (!open) {
            setAccessing(null)
            setReason('')
          }
        }}
        title="访问受控内容"
        description="访问原因会写入独立审计"
        footer={
          <>
            <Button variant="secondary" onClick={() => setAccessing(null)}>
              取消
            </Button>
            <Button
              disabled={!reason.trim() || access.isPending}
              onClick={() => {
                if (accessing) access.mutate({ id: accessing.id, reason })
              }}
            >
              查看内容
            </Button>
          </>
        }
      >
        <Field label="访问原因" htmlFor="content-reason">
          <Textarea
            id="content-reason"
            rows={4}
            value={reason}
            autoFocus
            onChange={(event) => setReason(event.target.value)}
          />
        </Field>
        <FormProblem error={access.error} />
      </DialogFrame>
      <DetailDrawer
        open={revealed !== null}
        onOpenChange={(open) => {
          if (!open) setRevealed(null)
        }}
        title="受控内容"
        subtitle={revealed?.requestId}
      >
        {revealed?.content ? (
          <div className="content-record">
            <section>
              <h2>请求</h2>
              <pre>{JSON.stringify(revealed.content.request, null, 2)}</pre>
            </section>
            <section>
              <h2>响应</h2>
              <pre>{JSON.stringify(revealed.content.response, null, 2)}</pre>
            </section>
          </div>
        ) : null}
      </DetailDrawer>
      <DialogFrame
        open={operation !== null}
        onOpenChange={(open) => {
          if (!open) setOperation(null)
        }}
        title="内容删除操作"
      >
        {operation ? <OperationPanel initial={operation} /> : null}
      </DialogFrame>
    </Page>
  )
}
