import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { CircleDot, Clock3, Network } from 'lucide-react'
import { useMemo, useState } from 'react'

import { accessApi, catalogApi, ledgerApi, type ListQuery, type RequestLog } from '@/api'
import { useSession } from '@/app/session'
import { DataTable, type ColumnDef } from '@/components/data-table/data-table'
import { TableToolbar } from '@/components/data-table/table-toolbar'
import { Page, PageHeader, PageSection } from '@/components/layout'
import { Badge, StatusBadge } from '@/components/ui/badge'
import { DetailDrawer } from '@/components/ui/detail-drawer'
import { NativeSelect } from '@/components/ui/field'
import { EmptyState, ErrorState, LoadingState } from '@/components/ui/state'
import { useListSearch } from '@/hooks/use-list-search'
import { formatDateTime, formatDuration, formatNumber } from '@/lib/format'

type Range = '24h' | '7d' | '30d'

export function UsagePage() {
  const session = useSession()
  const { state, setPage, setSearch, setStatus } = useListSearch()
  const [range, setRange] = useState<Range>('24h')
  const [userId, setUserId] = useState('')
  const [gatewayKeyId, setGatewayKeyId] = useState('')
  const [modelId, setModelId] = useState('')
  const [resourceDomain, setResourceDomain] = useState('')
  const [selected, setSelected] = useState<RequestLog | null>(null)
  const window = useMemo(() => requestWindow(range), [range])
  const filters: ListQuery = {
    ...state,
    ...window,
    ...(userId ? { userId } : {}),
    ...(gatewayKeyId ? { gatewayKeyId } : {}),
    ...(modelId ? { modelId } : {}),
    ...(resourceDomain ? { resourceDomain: resourceDomain as 'free' | 'professional' } : {}),
  }
  const query = useQuery({
    queryKey: ['request-logs', filters],
    queryFn: ({ signal }) => ledgerApi.requestLogs(filters, signal),
    placeholderData: keepPreviousData,
    refetchInterval: 15_000,
  })
  const users = useQuery({
    queryKey: ['users', 'request-log-filter'],
    queryFn: ({ signal }) => accessApi.users({ page: 1, pageSize: 100 }, signal),
    enabled: session.role === 'administrator',
  })
  const keys = useQuery({
    queryKey: ['gateway-keys', 'request-log-filter'],
    queryFn: ({ signal }) => accessApi.keys({ page: 1, pageSize: 100 }, signal),
  })
  const models = useQuery({
    queryKey: ['models', 'request-log-filter'],
    queryFn: ({ signal }) => catalogApi.models({ page: 1, pageSize: 100 }, signal),
    enabled: session.role === 'administrator',
  })
  const modelOptions = useMemo(() => {
    if (session.role === 'administrator') {
      return (models.data?.items ?? []).map((model) => ({ id: model.id, name: model.alias }))
    }
    const result = new Map<string, string>()
    for (const key of keys.data?.items ?? []) {
      key.authorizedModelIds.forEach((id, index) => {
        result.set(id, key.authorizedModels[index] ?? id)
      })
    }
    return Array.from(result, ([id, name]) => ({ id, name }))
  }, [keys.data?.items, models.data?.items, session.role])
  const columns = useMemo<ColumnDef<RequestLog, unknown>[]>(
    () => [
      {
        accessorKey: 'acceptedAt',
        header: '时间',
        cell: ({ row }) => formatDateTime(row.original.acceptedAt),
      },
      ...(session.role === 'member'
        ? []
        : [{ accessorKey: 'userName', header: '成员' } as ColumnDef<RequestLog, unknown>]),
      {
        accessorKey: 'keyPrefix',
        header: 'Gateway Key',
        cell: ({ row }) => <code>{row.original.keyPrefix}…</code>,
      },
      { accessorKey: 'modelAlias', header: '模型' },
      {
        id: 'tokens',
        header: 'Token',
        cell: ({ row }) => tokenSummary(row.original),
      },
      {
        id: 'latency',
        header: '耗时',
        cell: ({ row }) => formatDuration(requestDuration(row.original)),
      },
      {
        accessorKey: 'status',
        header: '状态',
        cell: ({ row }) => <StatusBadge status={row.original.status} />,
      },
      {
        accessorKey: 'requestId',
        header: 'Request ID',
        cell: ({ row }) => <code>{row.original.requestId}</code>,
      },
    ],
    [session.role],
  )

  const resetPage = (change: () => void) => {
    change()
    setPage(1)
  }

  return (
    <Page>
      <PageHeader title="API 日志" />
      <PageSection>
        <TableToolbar
          search={state.search}
          onSearchChange={setSearch}
          searchLabel="搜索成员、Key、模型或 Request ID"
          status={state.status}
          onStatusChange={setStatus}
          statusOptions={[
            { value: 'queued', label: '排队中' },
            { value: 'dispatching', label: '发送中' },
            { value: 'streaming', label: '流式返回' },
            { value: 'completed', label: '已完成' },
            { value: 'failed', label: '失败' },
            { value: 'canceled', label: '已取消' },
            { value: 'uncertain', label: '待确认' },
          ]}
          filters={
            <>
              <NativeSelect
                aria-label="时间范围"
                value={range}
                onChange={(event) => resetPage(() => setRange(event.target.value as Range))}
              >
                <option value="24h">最近 24 小时</option>
                <option value="7d">最近 7 天</option>
                <option value="30d">最近 30 天</option>
              </NativeSelect>
              {session.role === 'administrator' ? (
                <NativeSelect
                  aria-label="成员筛选"
                  value={userId}
                  onChange={(event) => resetPage(() => setUserId(event.target.value))}
                >
                  <option value="">全部成员</option>
                  {(users.data?.items ?? []).map((user) => (
                    <option key={user.id} value={user.id}>
                      {user.displayName}
                    </option>
                  ))}
                </NativeSelect>
              ) : null}
              <NativeSelect
                aria-label="Gateway Key 筛选"
                value={gatewayKeyId}
                onChange={(event) => resetPage(() => setGatewayKeyId(event.target.value))}
              >
                <option value="">全部 Gateway Key</option>
                {(keys.data?.items ?? []).map((key) => (
                  <option key={key.id} value={key.id}>
                    {key.name} · {key.prefix}…
                  </option>
                ))}
              </NativeSelect>
              <NativeSelect
                aria-label="模型筛选"
                value={modelId}
                onChange={(event) => resetPage(() => setModelId(event.target.value))}
              >
                <option value="">全部模型</option>
                {modelOptions.map((model) => (
                  <option key={model.id} value={model.id}>
                    {model.name}
                  </option>
                ))}
              </NativeSelect>
              <NativeSelect
                aria-label="资源域筛选"
                value={resourceDomain}
                onChange={(event) => resetPage(() => setResourceDomain(event.target.value))}
              >
                <option value="">全部资源域</option>
                <option value="free">免费</option>
                <option value="professional">专业</option>
              </NativeSelect>
            </>
          }
        />
        <DataTable
          ariaLabel="API 日志列表"
          data={query.data?.items ?? []}
          columns={columns}
          getRowId={(record) => record.id}
          loading={query.isLoading}
          fetching={query.isFetching}
          error={query.error}
          onRetry={() => void query.refetch()}
          emptyLabel="当前范围没有请求记录"
          page={query.data?.page ?? state.page}
          pageSize={query.data?.pageSize ?? state.pageSize}
          total={query.data?.total ?? 0}
          onPageChange={setPage}
          onRowClick={setSelected}
          renderMobile={(record) => (
            <div className="mobile-summary">
              <div>
                <strong>{record.modelAlias}</strong>
                <StatusBadge status={record.status} />
              </div>
              <span>
                {formatDateTime(record.acceptedAt)} · {record.keyPrefix}…
              </span>
              <span>
                {tokenSummary(record)} · {formatDuration(requestDuration(record))}
              </span>
            </div>
          )}
        />
      </PageSection>
      <RequestDetail request={selected} onOpenChange={(open) => !open && setSelected(null)} />
    </Page>
  )
}

function RequestDetail({
  request,
  onOpenChange,
}: {
  request: RequestLog | null
  onOpenChange: (open: boolean) => void
}) {
  const detail = useQuery({
    queryKey: ['request-log', request?.requestId],
    queryFn: ({ signal }) => ledgerApi.requestLog(request?.requestId ?? '', signal),
    enabled: request !== null,
    refetchInterval: request && isTerminal(request.status) ? false : 3_000,
  })
  return (
    <DetailDrawer
      open={request !== null}
      onOpenChange={onOpenChange}
      title="请求详情"
      subtitle={request?.requestId}
    >
      {detail.isLoading ? (
        <LoadingState label="正在读取请求详情" />
      ) : detail.error || !detail.data ? (
        <ErrorState error={detail.error} onRetry={() => void detail.refetch()} />
      ) : (
        <div className="detail-stack">
          <div className="detail-band">
            <header>
              <h2>
                <CircleDot size={16} /> 请求事实
              </h2>
              <StatusBadge status={detail.data.request.status} />
            </header>
            <dl className="fact-grid">
              <Fact label="模型" value={detail.data.request.modelAlias} />
              <Fact label="Gateway Key" value={`${detail.data.request.keyPrefix}…`} mono />
              <Fact label="接受时间" value={formatDateTime(detail.data.request.acceptedAt)} />
              <Fact label="耗时" value={formatDuration(requestDuration(detail.data.request))} />
              <Fact label="输入 Token" value={tokenValue(detail.data.request.inputTokens)} />
              <Fact label="输出 Token" value={tokenValue(detail.data.request.outputTokens)} />
              <Fact label="用量来源" value={usageSourceLabel[detail.data.request.usageSource]} />
              <Fact label="响应方式" value={detail.data.request.stream ? '流式' : '非流式'} />
            </dl>
          </div>
          {detail.data.request.errorKind ? (
            <div className="detail-band detail-band--error">
              <header>
                <h2>
                  <Network size={16} /> 稳定错误类别
                </h2>
              </header>
              <code>{detail.data.request.errorKind}</code>
            </div>
          ) : null}
          <div className="detail-band">
            <header>
              <h2>
                <Clock3 size={16} /> 发送边界
              </h2>
              <Badge>{detail.data.attempts.length} 次 attempt</Badge>
            </header>
            {detail.data.attempts.length === 0 ? (
              <EmptyState title="请求尚未发送到上游" />
            ) : (
              <ol className="attempt-timeline">
                {detail.data.attempts.map((attempt) => (
                  <li key={attempt.id}>
                    <span className="attempt-timeline__marker">{attempt.sequence}</span>
                    <div className="attempt-timeline__body">
                      <div>
                        <strong>
                          {attempt.providerName ?? '统一请求执行'}
                          {attempt.credentialName ? ` · ${attempt.credentialName}` : ''}
                        </strong>
                        <StatusBadge status={attempt.status} />
                      </div>
                      <dl>
                        <Fact label="创建" value={formatDateTime(attempt.createdAt)} />
                        <Fact label="已发送" value={formatDateTime(attempt.sentAt)} />
                        <Fact label="首字节" value={formatDateTime(attempt.firstByteAt)} />
                        <Fact label="完成" value={formatDateTime(attempt.completedAt)} />
                        <Fact label="HTTP" value={attempt.httpStatus?.toString() ?? '—'} />
                        <Fact label="错误" value={attempt.errorKind ?? '—'} mono />
                      </dl>
                    </div>
                  </li>
                ))}
              </ol>
            )}
          </div>
        </div>
      )}
    </DetailDrawer>
  )
}

function Fact({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) {
  return (
    <div>
      <dt>{label}</dt>
      <dd>{mono ? <code>{value}</code> : value}</dd>
    </div>
  )
}

function requestWindow(range: Range): Pick<ListQuery, 'from' | 'to'> {
  const to = new Date()
  const hours = range === '24h' ? 24 : range === '7d' ? 7 * 24 : 30 * 24
  const from = new Date(to.getTime() - hours * 60 * 60 * 1000)
  return { from: from.toISOString(), to: to.toISOString() }
}

function requestDuration(request: RequestLog): number {
  return Math.max(
    0,
    new Date(request.completedAt ?? request.updatedAt).getTime() -
      new Date(request.acceptedAt).getTime(),
  )
}

function tokenSummary(request: RequestLog): string {
  if (request.inputTokens === undefined || request.outputTokens === undefined) return 'Token 未知'
  return `${formatNumber(request.inputTokens)} / ${formatNumber(request.outputTokens)}`
}

function tokenValue(value: number | undefined): string {
  return value === undefined ? '未知' : formatNumber(value)
}

function isTerminal(status: RequestLog['status']): boolean {
  return ['completed', 'failed', 'canceled', 'uncertain'].includes(status)
}

const usageSourceLabel = {
  authoritative: '上游权威',
  estimated: '本地估算',
  unknown: '未知',
} as const
