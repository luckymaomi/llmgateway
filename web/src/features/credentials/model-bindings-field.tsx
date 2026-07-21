import type { CredentialModelBinding, Model } from '@/api'
import { Input } from '@/components/ui/field'

type EditableBinding = Omit<CredentialModelBinding, 'modelName'>

export function ModelBindingsField({
  id,
  models,
  value,
  disabled,
  onChange,
}: {
  id: string
  models: Model[]
  value: EditableBinding[]
  disabled: boolean
  onChange: (value: EditableBinding[]) => void
}) {
  return (
    <div id={id} className="model-bindings">
      {models.map((model) => {
        const binding = value.find((item) => item.modelId === model.id)
        return (
          <div className="model-binding" key={model.id} data-selected={Boolean(binding)}>
            <label className="model-binding__choice">
              <input
                type="checkbox"
                checked={Boolean(binding)}
                disabled={disabled}
                onChange={(event) => {
                  onChange(
                    event.currentTarget.checked
                      ? [...value, { modelId: model.id, priority: 100, weight: 100 }]
                      : value.filter((item) => item.modelId !== model.id),
                  )
                }}
              />
              <span>{model.alias}</span>
            </label>
            <div className="model-binding__routing">
              <label>
                <span>优先级</span>
                <Input
                  type="number"
                  min={0}
                  max={1000}
                  aria-label={`${model.alias} 优先级`}
                  value={binding?.priority ?? 100}
                  disabled={disabled || !binding}
                  onChange={(event) =>
                    updateBinding(
                      value,
                      model.id,
                      { priority: event.currentTarget.valueAsNumber },
                      onChange,
                    )
                  }
                />
              </label>
              <label>
                <span>权重</span>
                <Input
                  type="number"
                  min={1}
                  max={1000}
                  aria-label={`${model.alias} 权重`}
                  value={binding?.weight ?? 100}
                  disabled={disabled || !binding}
                  onChange={(event) =>
                    updateBinding(
                      value,
                      model.id,
                      { weight: event.currentTarget.valueAsNumber },
                      onChange,
                    )
                  }
                />
              </label>
            </div>
          </div>
        )
      })}
    </div>
  )
}

function updateBinding(
  bindings: EditableBinding[],
  modelId: string,
  change: Partial<Pick<EditableBinding, 'priority' | 'weight'>>,
  onChange: (value: EditableBinding[]) => void,
): void {
  onChange(
    bindings.map((binding) => (binding.modelId === modelId ? { ...binding, ...change } : binding)),
  )
}
