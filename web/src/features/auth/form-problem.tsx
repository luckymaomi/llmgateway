import { ApiProblem } from '@/api'

export function FormProblem({ error }: { error: unknown }) {
  if (!error) return null
  const problem = error instanceof ApiProblem ? error : null
  return (
    <div className="form-problem" role="alert">
      <strong>{problem?.message ?? '提交未完成'}</strong>
      {problem?.requestId ? <span>Request ID：{problem.requestId}</span> : null}
    </div>
  )
}
