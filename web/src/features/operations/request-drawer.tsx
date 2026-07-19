import { useQuery } from '@tanstack/react-query'
import { AlertTriangle, ArrowRight, Clock3, Route, Sigma } from 'lucide-react'

import { operationsApi, type GatewayRequest } from '@/api'
import { Badge, StatusBadge } from '@/components/ui/badge'
import { DetailDrawer } from '@/components/ui/detail-drawer'
import { ErrorState, LoadingState } from '@/components/ui/state'
import { formatDateTime, formatDuration, formatNumber } from '@/lib/format'

export function RequestDrawer({
  requestId,
  onOpenChange,
}: {
  requestId: string | null
  onOpenChange: (open: boolean) => void
}) {
  const query = useQuery({
    queryKey: ['request-detail', requestId],
    queryFn: ({ signal }) => operationsApi.request(requestId ?? '', signal),
    enabled: Boolean(requestId),
  })
  return (
    <DetailDrawer
      open={Boolean(requestId)}
      onOpenChange={onOpenChange}
      title="请求详情"
      subtitle={query.data?.requestId ?? requestId ?? ''}
    >
      {query.isLoading ? (
        <LoadingState label="正在加载请求事实" />
      ) : query.error || !query.data ? (
        <ErrorState error={query.error} onRetry={() => void query.refetch()} />
      ) : (
        <RequestFacts request={query.data} />
      )}
    </DetailDrawer>
  )
}

function RequestFacts({ request }: { request: GatewayRequest }) {
  return (
    <div className="detail-stack">
      <section className="detail-band">
        <header>
          <h2>执行事实</h2>
          <StatusBadge status={request.state} />
        </header>
        <dl className="fact-grid">
          <div>
            <dt>用户</dt>
            <dd>{request.userName}</dd>
          </div>
          <div>
            <dt>Key</dt>
            <dd>
              <code>{request.keyPrefix}…</code>
            </dd>
          </div>
          <div>
            <dt>模型</dt>
            <dd>{request.modelAlias}</dd>
          </div>
          <div>
            <dt>资源域</dt>
            <dd>
              <Badge tone={request.resourceDomain === 'free' ? 'positive' : 'info'}>
                {request.resourceDomain === 'free' ? '免费' : '专业'}
              </Badge>
            </dd>
          </div>
          <div>
            <dt>创建</dt>
            <dd>{formatDateTime(request.createdAt)}</dd>
          </div>
          <div>
            <dt>完成</dt>
            <dd>{formatDateTime(request.completedAt)}</dd>
          </div>
          <div>
            <dt>总耗时</dt>
            <dd>{formatDuration(request.latencyMs)}</dd>
          </div>
          <div>
            <dt>首 Token</dt>
            <dd>{formatDuration(request.ttftMs)}</dd>
          </div>
          <div>
            <dt>输入 Token</dt>
            <dd>
              {request.inputTokens === undefined ? '未知' : formatNumber(request.inputTokens)}
            </dd>
          </div>
          <div>
            <dt>输出 Token</dt>
            <dd>
              {request.outputTokens === undefined ? '未知' : formatNumber(request.outputTokens)}
            </dd>
          </div>
          <div>
            <dt>配置版本</dt>
            <dd>
              <code>{request.configurationRevisionId}</code>
            </dd>
          </div>
        </dl>
      </section>

      {request.errorCode ? (
        <section className="detail-band detail-band--error">
          <header>
            <h2>
              <AlertTriangle size={17} />
              错误
            </h2>
          </header>
          <strong>{request.errorMessage}</strong>
          <code>{request.errorCode}</code>
        </section>
      ) : null}

      <section className="detail-band">
        <header>
          <h2>
            <Route size={17} />
            Attempt 时间线
          </h2>
          <span>{request.attempts?.length ?? 0} 次</span>
        </header>
        <ol className="attempt-timeline">
          {request.attempts?.map((attempt) => (
            <li key={attempt.id}>
              <div className="attempt-timeline__marker">{attempt.sequence}</div>
              <div className="attempt-timeline__body">
                <div>
                  <strong>{attempt.providerName}</strong>
                  <ArrowRight size={14} />
                  <span>{attempt.credentialLabel}</span>
                  <StatusBadge status={attempt.state} />
                </div>
                <dl>
                  <div>
                    <Clock3 size={13} />
                    {formatDuration(attempt.latencyMs)}
                  </div>
                  <div>
                    <Sigma size={13} />
                    {attempt.inputTokens ?? 0} + {attempt.outputTokens ?? 0} Token
                  </div>
                  <div>{formatDateTime(attempt.startedAt)}</div>
                </dl>
                {attempt.exclusionReasons.length > 0 ? (
                  <div className="exclusion-list">
                    {attempt.exclusionReasons.map((reason) => (
                      <Badge key={reason} tone="warning">
                        {reason}
                      </Badge>
                    ))}
                  </div>
                ) : null}
                {attempt.errorCode ? <code>{attempt.errorCode}</code> : null}
              </div>
            </li>
          )) ?? <li>没有 attempt 记录</li>}
        </ol>
      </section>
    </div>
  )
}
