import { createContext, useContext } from 'react'

export type OnboardingRoute =
  '/resource-pools' | '/provider-keys' | '/plans' | '/members' | '/subscriptions' | '/api-keys'

export interface OnboardingTarget {
  label: string
  to: OnboardingRoute
  action: string
  actionLabel: string
}

export interface OnboardingTourValue {
  startTour: (target: OnboardingTarget) => void
}

export const OnboardingTourContext = createContext<OnboardingTourValue | null>(null)

export function useOnboardingTour() {
  const value = useContext(OnboardingTourContext)
  if (!value) throw new Error('Onboarding tour must be rendered inside the application shell')
  return value
}
