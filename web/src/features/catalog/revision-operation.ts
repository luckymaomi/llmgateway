import type { ConfigurationRevision, OperationSnapshot } from '@/api'

export type CompletedRevisionResult =
  | { kind: 'capture'; revision: ConfigurationRevision }
  | { kind: 'validate' | 'publish' | 'rollback'; operation: OperationSnapshot }

export function completedRevisionTitle(result: CompletedRevisionResult): string {
  switch (result.kind) {
    case 'capture':
      return '配置已捕获'
    case 'validate':
      return '校验完成'
    case 'publish':
      return '配置已发布'
    case 'rollback':
      return '回滚完成'
  }
}
