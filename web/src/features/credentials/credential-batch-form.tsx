import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useMemo, useState, type FormEvent } from 'react'

import { catalogApi, type CredentialBatchResult } from '@/api'
import { Button } from '@/components/ui/button'
import { DialogFrame } from '@/components/ui/dialog'
import { Field, NativeSelect, Textarea } from '@/components/ui/field'
import { FormProblem } from '@/features/auth/form-problem'

export function CredentialBatchForm({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const queryClient = useQueryClient()
  const [resourcePoolId, setResourcePoolId] = useState('')
  const [lines, setLines] = useState('')
  const [results, setResults] = useState<CredentialBatchResult[]>()
  const pools = useQuery({
    queryKey: ['resource-pools', 'credential-batch'],
    queryFn: ({ signal }) => catalogApi.resourcePools(false, signal),
    enabled: open,
  })
  const pool = useMemo(
    () => pools.data?.find((item) => item.id === resourcePoolId),
    [pools.data, resourcePoolId],
  )
  const mutation = useMutation({
    mutationFn: () => {
      if (!pool) throw new Error('请选择资源池')
      const items = parseLines(lines)
      return catalogApi.importCredentials(
        {
          resourcePoolId,
          items,
          modelBindings: pool.models.map((model) => ({
            modelId: model.id,
            priority: 0,
            weight: 1,
          })),
        },
        crypto.randomUUID(),
      )
    },
    async onSuccess(value) {
      setLines('')
      setResults(value)
      await queryClient.invalidateQueries({ queryKey: ['credentials'] })
      await queryClient.invalidateQueries({ queryKey: ['resource-pools'] })
    },
  })

  function close() {
    if (mutation.isPending) return
    setResults(undefined)
    mutation.reset()
    onOpenChange(false)
  }

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!resourcePoolId || parseLines(lines).length === 0) return
    mutation.mutate()
  }

  return (
    <DialogFrame
      open={open}
      onOpenChange={(next) => !next && close()}
      title={results ? '批量导入结果' : '批量导入上游 API Key'}
      width="lg"
      dismissible={!mutation.isPending}
      footer={
        results ? (
          <Button onClick={close}>完成</Button>
        ) : (
          <>
            <Button variant="secondary" type="button" disabled={mutation.isPending} onClick={close}>
              取消
            </Button>
            <Button type="submit" form="credential-batch-form" disabled={mutation.isPending}>
              {mutation.isPending ? '导入中' : '开始导入'}
            </Button>
          </>
        )
      }
    >
      {results ? (
        <div className="batch-results">
          {results.map((result) => (
            <div key={`${result.line}-${result.name}`} data-status={result.status}>
              <span>第 {result.line} 行</span>
              <strong>{result.name}</strong>
              <span>{batchStatus[result.status]}</span>
              <code>{result.errorKind ?? ''}</code>
            </div>
          ))}
        </div>
      ) : (
        <form id="credential-batch-form" className="form-grid" onSubmit={submit}>
          <Field label="资源池" htmlFor="batch-pool" className="field--full">
            <NativeSelect
              id="batch-pool"
              autoFocus
              value={resourcePoolId}
              disabled={mutation.isPending}
              onChange={(event) => setResourcePoolId(event.target.value)}
            >
              <option value="">请选择</option>
              {(pools.data ?? []).map((item) => (
                <option key={item.id} value={item.id}>
                  {item.providerName} · {item.name}
                </option>
              ))}
            </NativeSelect>
          </Field>
          <Field
            label="逐行粘贴"
            htmlFor="batch-lines"
            className="field--full"
            hint="每行一个 Key；也可使用 名称,Key"
          >
            <Textarea
              id="batch-lines"
              rows={12}
              value={lines}
              readOnly={mutation.isPending}
              onChange={(event) => setLines(event.target.value)}
            />
          </Field>
          <FormProblem error={mutation.error ?? pools.error} />
        </form>
      )}
    </DialogFrame>
  )
}

function parseLines(value: string): Array<{ name: string; secret: string }> {
  return value
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean)
    .map((line, index) => {
      const separator = line.indexOf(',')
      return separator > 0
        ? { name: line.slice(0, separator).trim(), secret: line.slice(separator + 1).trim() }
        : { name: `Key ${index + 1}`, secret: line }
    })
    .filter((item) => item.secret.length > 0)
}

const batchStatus = { created: '已创建', skipped: '已跳过', rejected: '已拒绝' } as const
