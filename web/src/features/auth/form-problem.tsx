import { ApiProblem } from '@/api'

export function FormProblem({ error }: { error: unknown }) {
  if (!error) return null
  const problem = error instanceof ApiProblem ? error : null
  return (
    <div className="form-problem" role="alert">
      <strong>{problem ? problemMessage(problem.code, problem.message) : '提交未完成'}</strong>
      {problem?.requestId ? <span>Request ID：{problem.requestId}</span> : null}
    </div>
  )
}

function problemMessage(code: string, fallback: string): string {
  if (code === 'conflict') return '数据已被其他操作更新，请确认最新事实后再试。'
  if (code === 'provider_must_be_disabled') return '请先停用 Provider，再修改类型或 Base URL。'
  if (code === 'registry_validation_unavailable') return '暂时无法核验 Provider 地址，请稍后重试。'
  if (code === 'approval_required') return '账号正在等待管理员审核。'
  return fallback
}
