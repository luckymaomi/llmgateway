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
  if (code === 'invalid_input') return '填写内容不符合要求，请检查后再试。'
  if (code === 'forbidden') return '当前账号不能执行这项操作。'
  if (code === 'not_found') return '要操作的内容已不存在，请刷新后再试。'
  return fallback
}
