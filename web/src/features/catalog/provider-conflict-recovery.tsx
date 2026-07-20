import type { ProviderRecord } from '@/api'

import {
  buildProviderRebase,
  type ProviderConflictChoice,
  type ProviderConflictState,
  type ProviderEditableField,
  type ProviderFieldDifference,
} from './provider-conflict-rebase'

export function ProviderConflictRecovery({
  state,
  onChoice,
}: {
  state: ProviderConflictState & { latest: ProviderRecord }
  onChoice: (field: ProviderEditableField, choice: ProviderConflictChoice) => void
}) {
  const rebase = buildProviderRebase(state.opened, state.draft, state.latest, state.choices)

  return (
    <section className="provider-conflict" aria-labelledby="provider-conflict-title">
      <header className="provider-conflict__header">
        <h3 id="provider-conflict-title">合并并发修改</h3>
        <p>不重叠字段已自动合并；同一字段的不同修改需要逐项确认。</p>
      </header>
      {rebase.differences.length > 0 ? (
        <div>
          {rebase.differences.map((difference) => (
            <fieldset className="provider-conflict__field" key={difference.field}>
              <legend>{difference.label}</legend>
              <div className="provider-conflict__values">
                <div>
                  <span>你的草稿</span>
                  <code>{displayValue(difference.field, difference.draftValue)}</code>
                </div>
                <div>
                  <span>当前最新</span>
                  <code>{displayValue(difference.field, difference.latestValue)}</code>
                </div>
              </div>
              {difference.reason === 'overlap' ? (
                <div className="provider-conflict__choices">
                  <label data-selected={difference.choice === 'draft' ? 'true' : undefined}>
                    <input
                      type="radio"
                      name={`provider-conflict-${difference.field}`}
                      checked={difference.choice === 'draft'}
                      onChange={() => onChoice(difference.field, 'draft')}
                    />
                    <span>保留草稿</span>
                  </label>
                  <label data-selected={difference.choice === 'latest' ? 'true' : undefined}>
                    <input
                      type="radio"
                      name={`provider-conflict-${difference.field}`}
                      checked={difference.choice === 'latest'}
                      onChange={() => onChoice(difference.field, 'latest')}
                    />
                    <span>采用最新</span>
                  </label>
                </div>
              ) : (
                <p className="provider-conflict__result">
                  {automaticMergeLabel(difference.reason)}
                </p>
              )}
            </fieldset>
          ))}
        </div>
      ) : (
        <p className="provider-conflict__same">可编辑字段没有差异，可以按最新版本保存。</p>
      )}
    </section>
  )
}

function automaticMergeLabel(
  reason: Exclude<ProviderFieldDifference['reason'], 'overlap'>,
): string {
  switch (reason) {
    case 'draft-only':
      return '自动合并：保留你的草稿'
    case 'latest-only':
      return '自动合并：采用当前最新值'
    case 'routing-locked':
      return 'Provider 已启用：路由字段采用当前最新值'
  }
}

function displayValue(field: ProviderEditableField, value: string): string {
  if (field !== 'kind') return value
  switch (value) {
    case 'openai-compatible':
      return 'OpenAI-compatible'
    case 'zhipu':
      return '智谱 GLM'
    case 'deepseek':
      return 'DeepSeek'
    case 'agnes':
      return 'Agnes'
    default:
      return value
  }
}
